package broker

import (
	"context"
	"fmt"
	"github.com/seaweedfs/seaweedfs/weed/glog"
	"github.com/seaweedfs/seaweedfs/weed/mq/topic"
	"github.com/seaweedfs/seaweedfs/weed/pb/filer_pb"
	"github.com/seaweedfs/seaweedfs/weed/pb/mq_pb"
	"time"
)

func (b *MessageQueueBroker) SubscribeMessage(req *mq_pb.SubscribeMessageRequest, stream mq_pb.SeaweedMessaging_SubscribeMessageServer) error {

	t := topic.FromPbTopic(req.GetInit().Topic)
	partition := topic.FromPbPartition(req.GetInit().GetPartitionOffset().GetPartition())
	localTopicPartition := b.localTopicManager.GetTopicPartition(t, partition)
	if localTopicPartition == nil {
		stream.Send(&mq_pb.SubscribeMessageResponse{
			Message: &mq_pb.SubscribeMessageResponse_Ctrl{
				Ctrl: &mq_pb.SubscribeMessageResponse_CtrlMessage{
					Error: "not initialized",
				},
			},
		})
		return nil
	}

	clientName := fmt.Sprintf("%s/%s-%s", req.GetInit().ConsumerGroup, req.GetInit().ConsumerId, req.GetInit().ClientId)
	localTopicPartition.Subscribers.AddSubscriber(clientName, topic.NewLocalSubscriber())
	glog.V(0).Infof("Subscriber %s connected on %v %v", clientName, t, partition)
	isConnected := true
	sleepIntervalCount := 0
	defer func() {
		isConnected = false
		localTopicPartition.Subscribers.RemoveSubscriber(clientName)
		glog.V(0).Infof("Subscriber %s on %v %v disconnected", clientName, t, partition)
	}()

	ctx := stream.Context()
	startTime := time.Now()
	if startTs := req.GetInit().GetPartitionOffset().GetTsNs(); startTs > 0 {
		startTime = time.Unix(0, startTs)
	}

	localTopicPartition.Subscribe(clientName, startTime, func() bool {
		if !isConnected {
			return false
		}
		sleepIntervalCount++
		if sleepIntervalCount > 10 {
			sleepIntervalCount = 10
		}
		time.Sleep(time.Duration(sleepIntervalCount) * 2339 * time.Millisecond)

		// Check if the client has disconnected by monitoring the context
		select {
		case <-ctx.Done():
			err := ctx.Err()
			if err == context.Canceled {
				// Client disconnected
				return false
			}
			glog.V(0).Infof("Subscriber %s disconnected: %v", clientName, err)
			return false
		default:
			// Continue processing the request
		}

		return true
	}, func(logEntry *filer_pb.LogEntry) error {
		// reset the sleep interval count
		sleepIntervalCount = 0

		value := logEntry.GetData()
		if err := stream.Send(&mq_pb.SubscribeMessageResponse{Message: &mq_pb.SubscribeMessageResponse_Data{
			Data: &mq_pb.DataMessage{
				Key:   []byte(fmt.Sprintf("key-%d", logEntry.PartitionKeyHash)),
				Value: value,
				TsNs:  logEntry.TsNs,
			},
		}}); err != nil {
			glog.Errorf("Error sending setup response: %v", err)
			return err
		}
		return nil
	})

	return nil
}

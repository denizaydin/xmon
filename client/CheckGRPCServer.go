package client

import (
	"context"
	"time"

	"github.com/denizaydin/xmon/proto"
	"google.golang.org/grpc"
)

//CheckGRPCServer - Send unary echo requests to the destination server for each interval.
func CheckGRPCServer(grpcDest *MonObject, client *XmonClient) {
	grpcDest.ThreadupdateTime = time.Now()
	client.Logging.Infof("grpc client:%v destination:%v, initial values:%v", grpcDest.Object.GetAppdest().GetName(), grpcDest.Object.GetAppdest().GetDestination(), grpcDest.Object.GetAppdest())
	exit := false
	conn, err := grpc.Dial(grpcDest.Object.GetAppdest().GetDestination(), grpc.WithInsecure())
	if err != nil {
		//can not dial to remote destination. Calling program should rewoke. Retry count may be added.
		client.Logging.Errorf("grpc client:%v destination:%v, did not connect:%v, exiting", grpcDest.Object.GetAppdest().GetName(), grpcDest.Object.GetAppdest().GetDestination(), err)
		return
	}
	defer conn.Close()
	for !exit {
		select {
		case <-grpcDest.Notify:
			client.Logging.Debugf("grpc client:%v destination:%v, received stop request", grpcDest.Object.GetAppdest().GetName(), grpcDest.Object.GetAppdest().GetDestination())
			conn.Close()
			exit = true
		default:
			//As we are lots of error handling which can be changed during continuous ping process we MUST wait before looping. If not we MAY hit the same error in very short time:)
			grpcDest.ThreadInterval = grpcDest.Object.GetAppdest().GetInterval()
			time.Sleep(time.Duration(time.Duration(grpcDest.ThreadInterval) * time.Millisecond))
			grpcDest.ThreadupdateTime = time.Now()
			c := proto.NewUnaryEchoServiceClient(conn)
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(time.Duration(grpcDest.ThreadInterval)*time.Millisecond))
			defer cancel()
			client.Logging.Tracef("grpc client:%v  destination:%v, sending unary echo request", grpcDest.Object.GetAppdest().GetName(), grpcDest.Object.GetAppdest().GetDestination())
			res, err := c.UnaryEcho(ctx, &proto.XmonEchoRequest{
				ClientSendTimestamp:    time.Now().UnixNano(),
				ServerReceiveTimestamp: 0,
				ServerSendTimestamp:    0,
				ClientReceiveTimestamp: 0,
			})
			if err != nil {
				client.Logging.Errorf("grpc client:%v  destination:%v, unexpected error from UnaryEcho:%v", grpcDest.Object.GetAppdest().GetName(), grpcDest.Object.GetAppdest().GetDestination(), err)
				//context error. Calling program should rewoke. Retry count may be added.
				return
			}
			responsetime := time.Now().UnixNano()
			stat := &proto.StatsObject{
				Client:    client.StatsClient,
				Timestamp: time.Now().UnixNano(),
				Object: &proto.StatsObject_Appstat{Appstat: &proto.AppStat{
					Destination:            grpcDest.Object.GetAppdest().GetDestination(),
					ClientSendTimestamp:    res.ClientSendTimestamp,
					ServerReceiveTimestamp: res.ServerReceiveTimestamp,
					ServerSendTimestamp:    res.ServerSendTimestamp,
					ClientReceiveTimestamp: responsetime,
					ServerDelay:            res.ServerDelay,
					ClientNetworkDelay:     res.ServerReceiveTimestamp - res.ClientSendTimestamp,
					ServerNetworkDelay:     responsetime - res.ServerSendTimestamp,
					ServerResponseDelay:    res.ServerSendTimestamp - res.ServerReceiveTimestamp - res.ServerDelay,
				}},
			}
			select {
			case client.Statschannel <- stat:
				client.Logging.Debugf("grpc client:%v destination:%v, sent stats:%v", grpcDest.Object.GetAppdest().GetName(), grpcDest.Object.GetAppdest().GetDestination(), stat)
			default:
				client.Logging.Errorf("grpc client:%v destination:%v, can not send stats:%v", grpcDest.Object.GetAppdest().GetName(), grpcDest.Object.GetAppdest().GetDestination(), stat)
			}
			client.Logging.Tracef("grpc client:%v destination:%v, RPC response:%v", grpcDest.Object.GetAppdest().GetName(), grpcDest.Object.GetAppdest().GetDestination(), res)
		}
	}
	client.Logging.Infof("grpc client:%v destination:%v, exiting", grpcDest.Object.GetAppdest().GetName(), grpcDest.Object.GetAppdest().GetDestination())
}

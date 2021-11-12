package client

import (
	"context"
	"net"
	"time"

	proto "github.com/denizaydin/xmon/proto"
)

//CheckResolveDestination - Send DNS Responce queries for each interval.
func CheckResolveDestination(resolvedest *MonObject, client *XmonClient) {
	resolvedest.ThreadupdateTime = time.Now()
	client.Logging.Infof("resolver:%v destination:%v, start initial values:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), resolvedest.Object)
	//prevResolvedip - if its changed or not?
	prevResolvedip := ""
	//changed if its changed RTT will be multiplied by -1 each time.
	changed := int32(-1)
	exit := false
	for !exit {
		select {
		case <-resolvedest.Notify:
			client.Logging.Tracef("resolver:%v destination:%v, received stop request", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination())
			exit = true
		default:
			//As we are lots of error handling which can be changed during continuous ping process we MUST wait before looping. If not we MAY hit the same error in very short time:)
			resolvedest.ThreadInterval = resolvedest.Object.GetResolvedest().GetInterval()
			time.Sleep(time.Duration(time.Duration(resolvedest.ThreadInterval) * time.Millisecond))
			resolvedest.ThreadupdateTime = time.Now()
			if resolvedest.Object.GetResolvedest().GetResolver() != "" {
				client.Logging.Tracef("resolver:%v destination:%v, starting resolve using resolver:%v ", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), resolvedest.Object.GetResolvedest().Resolver)
				r := &net.Resolver{
					PreferGo: true,
					Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
						d := net.Dialer{
							Timeout: time.Millisecond * time.Duration(10000),
						}
						return d.DialContext(ctx, network, resolvedest.Object.GetResolvedest().Resolver)
					},
				}
				st := time.Now()
				client.Logging.Tracef("resolver:%v destination:%v, sending req to resolver:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), resolvedest.Object.GetResolvedest().GetResolver())
				resolvedest.ThreadupdateTime = time.Now()
				ips, err := r.LookupHost(context.Background(), resolvedest.Object.GetResolvedest().GetDestination())
				resolvedest.ThreadupdateTime = time.Now()
				diff := int64(-1)
				resolvedip := "unresolved"
				if err == nil {
					diff = time.Now().Sub(st).Milliseconds()
					client.Logging.Tracef("resolver:%v destination:%v, received response:%v from resolver:%v in:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), ips[0], resolvedest.Object.GetResolvedest().Resolver, diff)
					resolvedip = ips[0]
					match := false
					for _, ip := range ips {
						if prevResolvedip == ip {
							match = true
						}
					}
					if !match {
						changed = changed * -1
						client.Logging.Tracef("resolver:%v destination:%v, resolvedip is changed from:%v to:%v resolver:%v in:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), prevResolvedip, ips[0], resolvedest.Object.GetResolvedest().Resolver, diff)
					}
					prevResolvedip = resolvedip
					stat := &proto.StatsObject{
						Client:    client.StatsClient,
						Timestamp: time.Now().UnixMicro(),
						Object: &proto.StatsObject_Resolvestat{
							Resolvestat: &proto.ResolveStat{
								Destination: resolvedest.Object.GetResolvedest().GetDestination(),
								Rtt:         changed * int32(diff),
								Resolvedip:  resolvedip,
								Resolver:    resolvedest.Object.GetResolvedest().GetResolver(),
								Error:       false,
							},
						},
					}
					select {
					case client.Statschannel <- stat:
						client.Logging.Tracef("resolver:%v destination:%v, sent stats:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), stat)
					default:
						client.Logging.Errorf("resolver:%v destination:%v, can not send stats:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), stat)
					}
				} else {
					client.Logging.Tracef("resolver:%v destination:%v, no reponse received from resolver:%v, err:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), resolvedest.Object.GetResolvedest().GetResolver(), err)
					stat := &proto.StatsObject{
						Client:    client.StatsClient,
						Timestamp: time.Now().UnixMicro(),
						Object: &proto.StatsObject_Resolvestat{
							Resolvestat: &proto.ResolveStat{
								Destination: resolvedest.Object.GetResolvedest().GetDestination(),
								Rtt:         int32(diff),
								Resolvedip:  resolvedip,
								Resolver:    resolvedest.Object.GetResolvedest().GetResolver(),
								Error:       true,
							},
						},
					}
					select {
					case client.Statschannel <- stat:
						client.Logging.Tracef("resolver:%v destination:%v, sent stats:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), stat)
					default:
						client.Logging.Errorf("resolver:%v destination:%v, can not send stats:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), stat)
					}
				}
				resolvedest.ThreadupdateTime = time.Now()
			} else {
				client.Logging.Tracef("resolver:%v destination:%v,starting using resolver localhost ", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination())
				st := time.Now()
				client.Logging.Tracef("resolver:%v  destination:%v, sending req to resolver:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), resolvedest.Object.GetResolvedest().GetResolver())
				ips, err := net.LookupHost(resolvedest.Object.GetResolvedest().GetDestination())
				diff := int64(-1)
				resolvedip := "unresolved"
				if err == nil {
					diff = time.Now().Sub(st).Milliseconds()
					client.Logging.Tracef("resolver:%v destination:%v, received response:%v from resolver:%v in:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), ips[0], resolvedest.Object.GetResolvedest().GetResolver(), diff)

					resolvedip = ips[0]
					match := false
					for _, ip := range ips {
						if prevResolvedip == ip {
							match = true
						}
					}
					if !match {
						changed = changed * -1
						client.Logging.Tracef("resolver:%v destination:%v, resolvedip is changed from:%v to:%v resolver:%v in:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), prevResolvedip, ips[0], resolvedest.Object.GetResolvedest().Resolver, diff)
					}
					prevResolvedip = resolvedip
					stat := &proto.StatsObject{
						Client:    client.StatsClient,
						Timestamp: time.Now().UnixMicro(),
						Object: &proto.StatsObject_Resolvestat{
							Resolvestat: &proto.ResolveStat{
								Destination: resolvedest.Object.GetResolvedest().GetDestination(),
								Rtt:         changed * int32(diff),
								Resolvedip:  resolvedip,
								Resolver:    "localhost",
								Error:       false,
							},
						},
					}
					select {
					case client.Statschannel <- stat:
						client.Logging.Tracef("resolver:%v destination:%v, sent stats:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), stat)
					default:
						client.Logging.Errorf("resolver:%v destination:%v, can not send stats:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), stat)
					}
				} else {
					client.Logging.Tracef("resolver:%v destination:%v, no reponse received from resolver:%v, err:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), resolvedest.Object.GetResolvedest().GetResolver(), err)
					stat := &proto.StatsObject{
						Client:    client.StatsClient,
						Timestamp: time.Now().UnixMicro(),
						Object: &proto.StatsObject_Resolvestat{
							Resolvestat: &proto.ResolveStat{
								Destination: resolvedest.Object.GetResolvedest().GetDestination(),
								Rtt:         int32(diff),
								Resolvedip:  resolvedip,
								Resolver:    "localhost",
								Error:       true,
							},
						},
					}
					select {
					case client.Statschannel <- stat:
						client.Logging.Tracef("resolver:%v destination:%v, sent stats:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), stat)
					default:
						client.Logging.Errorf("resolver:%v destination:%v, can not send stats:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), stat)
					}
				}
				resolvedest.ThreadupdateTime = time.Now()
			}
			resolvedest.ThreadupdateTime = time.Now()
		}
	}
	client.Logging.Tracef("resolver:%v destination:%v, exiting", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination())
}

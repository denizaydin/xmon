package client

import (
	"context"
	"net"
	"time"

	proto "github.com/denizaydin/xmon/proto"
)

//CheckResolveDestination - Send DNS Responce queries for each interval.
func CheckResolveDestination(resolvedest *MonObject, c *XmonClient) {
	resolvedest.ThreadupdateTime = time.Now()
	c.Logging.Infof("resolver:%v destination:%v, start initial values:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), resolvedest.Object)
	exit := false
	for !exit {
		select {
		case <-resolvedest.Notify:
			c.Logging.Debugf("resolver:%v destination:%v, received stop request", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination())
			exit = true
		default:
			//As we are lots of error handling which can be changed during continuous ping process we MUST wait before looping. If not we MAY hit the same error in very short time:)
			resolvedest.ThreadInterval = resolvedest.Object.GetResolvedest().GetInterval()
			time.Sleep(time.Duration(time.Duration(resolvedest.ThreadInterval) * time.Millisecond))
			resolvedest.ThreadupdateTime = time.Now()
			if resolvedest.Object.GetResolvedest().GetResolver() != "" {
				c.Logging.Tracef("resolver:%v destination:%v, starting resolve using resolver:%v ", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), resolvedest.Object.GetResolvedest().Resolver)
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
				c.Logging.Tracef("resolver:%v destination:%v, sending req to resolver:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), resolvedest.Object.GetResolvedest().GetResolver())
				resolvedest.ThreadupdateTime = time.Now()
				ips, err := r.LookupHost(context.Background(), resolvedest.Object.GetResolvedest().GetDestination())
				resolvedest.ThreadupdateTime = time.Now()
				diff := int64(-1)
				resolvedip := "unresolved"
				if err == nil {
					diff = time.Now().Sub(st).Milliseconds()
					c.Logging.Debugf("resolver:%v destination:%v, received response:%v from resolver:%v in:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), ips[0], resolvedest.Object.GetResolvedest().Resolver, diff)
					resolvedip = ips[0]
					stat := &proto.StatsObject{
						Client:    c.StatsClient,
						Timestamp: time.Now().UnixNano(),
						Object: &proto.StatsObject_Resolvestat{
							Resolvestat: &proto.ResolveStat{
								Destination: resolvedest.Object.GetResolvedest().GetDestination(),
								Rtt:         int32(diff),
								Resolvedip:  resolvedip,
								Resolver:    resolvedest.Object.GetResolvedest().GetResolver(),
								Error:       false,
							},
						},
					}
					select {
					case c.Statschannel <- stat:
						c.Logging.Debugf("resolver:%v destination:%v, sent stats:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), stat)
					default:
						c.Logging.Errorf("resolver:%v destination:%v, can not send stats:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), stat)
					}
				} else {
					c.Logging.Debugf("resolver:%v destination:%v, no reponse received from resolver:%v, err:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), resolvedest.Object.GetResolvedest().GetResolver(), err)
					stat := &proto.StatsObject{
						Client:    c.StatsClient,
						Timestamp: time.Now().UnixNano(),
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
					case c.Statschannel <- stat:
						c.Logging.Debugf("resolver:%v destination:%v, sent stats:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), stat)
					default:
						c.Logging.Errorf("resolver:%v destination:%v, can not send stats:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), stat)
					}
				}
				resolvedest.ThreadupdateTime = time.Now()
			} else {
				c.Logging.Tracef("resolver:%v destination:%v,starting using resolver localhost ", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination())
				st := time.Now()
				c.Logging.Tracef("resolver:%v  destination:%v, sending req to resolver:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), resolvedest.Object.GetResolvedest().GetResolver())
				ips, err := net.LookupHost(resolvedest.Object.GetResolvedest().GetDestination())
				diff := int64(-1)
				resolvedip := "unresolved"
				if err == nil {
					diff = time.Now().Sub(st).Milliseconds()
					c.Logging.Debugf("resolver:%v destination:%v, received response:%v from resolver:%v in:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), ips[0], resolvedest.Object.GetResolvedest().GetResolver(), diff)
					resolvedip = ips[0]
					stat := &proto.StatsObject{
						Client:    c.StatsClient,
						Timestamp: time.Now().UnixNano(),
						Object: &proto.StatsObject_Resolvestat{
							Resolvestat: &proto.ResolveStat{
								Destination: resolvedest.Object.GetResolvedest().GetDestination(),
								Rtt:         int32(diff),
								Resolvedip:  resolvedip,
								Resolver:    "localhost",
								Error:       false,
							},
						},
					}
					select {
					case c.Statschannel <- stat:
						c.Logging.Debugf("resolver:%v destination:%v, sent stats:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), stat)
					default:
						c.Logging.Errorf("resolver:%v destination:%v, can not send stats:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), stat)
					}
				} else {
					c.Logging.Debugf("resolver:%v destination:%v, no reponse received from resolver:%v, err:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), resolvedest.Object.GetResolvedest().GetResolver(), err)
					stat := &proto.StatsObject{
						Client:    c.StatsClient,
						Timestamp: time.Now().UnixNano(),
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
					case c.Statschannel <- stat:
						c.Logging.Debugf("resolver:%v destination:%v, sent stats:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), stat)
					default:
						c.Logging.Errorf("resolver:%v destination:%v, can not send stats:%v", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination(), stat)
					}
				}
				resolvedest.ThreadupdateTime = time.Now()
			}
			resolvedest.ThreadupdateTime = time.Now()
		}
	}
	c.Logging.Debugf("resolver:%v destination:%v, exiting", resolvedest.Object.GetResolvedest().GetName(), resolvedest.Object.GetResolvedest().GetDestination())
}

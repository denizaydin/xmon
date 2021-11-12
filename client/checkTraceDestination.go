//Package client - includes all required monitoring objects for the client
package client

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/aeden/traceroute"
	proto "github.com/denizaydin/xmon/proto"
)

//CheckTraceDestination -
func CheckTraceDestination(tracedest *MonObject, client *XmonClient) {
	tracedest.ThreadupdateTime = time.Now()
	client.Logging.Infof("tracer:%v destination:%v, starting with initial values:%v", tracedest.Object.GetTracedest().GetName(), tracedest.Object.GetTracedest().GetDestination(), tracedest.Object)
	options := traceroute.TracerouteOptions{}
	var destination net.IP
	done := make(chan bool, 2)
	var waitGroup sync.WaitGroup
	intstatschannel := make(chan traceroute.TracerouteHop, 0)
	go func() {
		defer waitGroup.Done()
		waitGroup.Add(1)
		for {
			select {
			case <-done:
				client.Logging.Tracef("tracer:%v destination:%v, out from stats loop", tracedest.Object.GetTracedest().GetName(), tracedest.Object.GetTracedest().GetDestination())
				return
			case hop, ok := <-intstatschannel:
				if ok {
					tracedest.ThreadupdateTime = time.Now()
					stat := &proto.StatsObject{
						Client:    client.StatsClient,
						Timestamp: time.Now().UnixMicro(),
						Object: &proto.StatsObject_Tracestat{
							Tracestat: &proto.TraceStat{
								Destination: tracedest.Object.GetTracedest().GetDestination(),
								HopIP:       fmt.Sprintf("%v.%v.%v.%v", hop.Address[0], hop.Address[1], hop.Address[2], hop.Address[3]),
								HopTTL:      int32(hop.TTL),
								HopRTT:      int32(hop.ElapsedTime),
							},
						},
					}
					select {
					case client.Statschannel <- stat:
						client.Logging.Tracef("tracer:%v destination:%v, sent stats:%v", tracedest.Object.GetTracedest().GetName(), tracedest.Object.GetTracedest().GetDestination(), stat)
					default:
						client.Logging.Errorf("tracer:%v destination:%v, can not send stats:%v", tracedest.Object.GetTracedest().GetName(), tracedest.Object.GetTracedest().GetDestination(), stat)
					}
				}
			}
		}
	}()
	exit := false
	for !exit {
		select {
		case <-tracedest.Notify:
			client.Logging.Infof("tracer:%v destination%v, stop request", tracedest.Object.GetTracedest().GetName(), tracedest.Object.GetTracedest().GetDestination())
			close(intstatschannel)
			exit = true
		default:
			tracedest.ThreadInterval = tracedest.Object.GetTracedest().GetInterval()
			time.Sleep(time.Duration(time.Duration(tracedest.ThreadInterval) * time.Millisecond))
			tracedest.ThreadupdateTime = time.Now()
			destination = net.ParseIP(tracedest.Object.GetTracedest().GetDestination())
			if destination != nil {
				if len(destination.To4()) != net.IPv4len {
					client.Logging.Errorf("tracer:%v destination:%v, unimplemented destiantion type:%v", tracedest.Object.GetTracedest().GetName(), tracedest.Object.GetTracedest().GetDestination(), destination)
					continue
				}
			} else {
				ips, err := net.LookupHost(tracedest.Object.GetTracedest().GetDestination())
				if err == nil {
					destination = net.ParseIP(ips[0])
					client.Logging.Tracef("tracer:%v destination:%v, resolved as:%v", tracedest.Object.GetTracedest().GetName(), tracedest.Object.GetTracedest().GetDestination(), destination)
				} else {
					client.Logging.Errorf("tracer:%v destination:%v, resolve error for destination", tracedest.Object.GetTracedest().GetName(), tracedest.Object.GetTracedest().GetDestination())
					continue
				}
			}
			tracedest.ThreadupdateTime = time.Now()
			options.SetRetries(1)
			options.SetMaxHops(int(tracedest.Object.GetTracedest().GetTtl()))
			options.SetFirstHop(1) // Start from the default gw
			_, err := traceroute.Traceroute(destination.String(), &options, intstatschannel)
			if err != nil {
				client.Logging.Errorf("tracer:%v destination:%v, err:%v", tracedest.Object.GetTracedest().GetName(), tracedest.Object.GetTracedest().GetDestination(), err)
			}
			intstatschannel = make(chan traceroute.TracerouteHop, 0)
			tracedest.ThreadupdateTime = time.Now()
		}
	}
	done <- true
	waitGroup.Wait()
	close(done)
	client.Logging.Infof("tracer:%v destination:%v, exiting", tracedest.Object.GetTracedest().GetName(), tracedest.Object.GetTracedest().GetDestination())
}

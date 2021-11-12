package client

import (
	"context"
	"net"
	"os"
	"time"

	proto "github.com/denizaydin/xmon/proto"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

const (
	protocolICMP = 1
)

/*
CheckIcmpPing - sends continous ipv4 icmp for each interval.
	- Dynamic interval and packet size.
	- Do not fragment bit is setted.
	- Waits for the reply till the next interval, sends error stats in case of context timeout.
	- If got error, changes destination to "error" and stats as it is.
TODO:
    - Checking sequence (send and recived packet integrity)
*/
func CheckIcmpPing(pingdest *MonObject, client *XmonClient) {
	pingdest.ThreadupdateTime = time.Now()
	client.Logging.Infof("pinger:%v destination:%v, initial values:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), pingdest.Object.GetPingdest())
	//pid - used in icmp header
	pid := os.Getpid() & 0xffff
	//id - used in icmp header
	id := 254
	//seq - used in icmp header, incremented on each request
	seq := 0
	//packetSize - min packet size
	packetSize := 64
	//ttl - initial packet size
	ttl := 0
	//destination - resolved destination, can be changed on each request
	var destination net.IP
	//exit - exiting way of the probe loop
	exit := false
	//prove sould notify our thread update timer, in that way calling function knows that prove is still running.
	for !exit {
		select {
		case <-pingdest.Notify:
			client.Logging.Infof("pinger:%v received stop reques, exiting from the internal loop", pingdest.Object.GetPingdest().GetName())
			exit = true
		default:
			pingdest.ThreadupdateTime = time.Now()
			//As we are lots of error handling which can be changed during continuous ping process we MUST wait before looping. If not we MAY hit the same error in very short time:)
			pingdest.ThreadInterval = pingdest.Object.GetPingdest().GetInterval()
			//Probe set the intevarl at the begining. This prevents uninterupted running for the loop process while continue process on errors.
			time.Sleep(time.Duration(time.Duration(pingdest.ThreadInterval) * time.Millisecond))
			pingdest.ThreadupdateTime = time.Now()
			//Resolve destination if its not raw ip address
			destination = net.ParseIP(pingdest.Object.GetPingdest().GetDestination())
			if destination != nil {
				if len(destination.To4()) != net.IPv4len {
					client.Logging.Errorf("pinger:%v destination:%v, unimplemented destiantion type:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), destination)
					pingdest.ThreadupdateTime = time.Now()
					continue
				}
			} else {
				ctx, cancel := context.WithTimeout(context.TODO(), 1*time.Second)
				var r net.Resolver
				ips, err := r.LookupHost(ctx, pingdest.Object.GetPingdest().GetDestination())
				defer cancel() // important to avoid a resource leak
				pingdest.ThreadupdateTime = time.Now()
				if err == nil {
					destination = net.ParseIP(ips[0])
					client.Logging.Tracef("pinger:%v destination:%v, resolved as:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), destination)
				} else {
					client.Logging.Errorf("pinger:%v destination:%v, resolve error for destination", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination())
					stat := &proto.StatsObject{
						Client:    client.StatsClient,
						Timestamp: time.Now().UnixMicro(),
						Object: &proto.StatsObject_Pingstat{
							Pingstat: &proto.PingStat{
								Destination: pingdest.Object.GetPingdest().GetDestination(),
								Code:        2,
								Ttl:         0,
								Error:       true,
							},
						},
					}
					select {
					case client.Statschannel <- stat:
						client.Logging.Tracef("pinger:%v destination:%v, sent stats:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), stat)
					default:
						client.Logging.Errorf("pinger:%v destination:%v, can not send stats:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), stat)
					}
					pingdest.ThreadupdateTime = time.Now()
					continue
				}
			}
			pingdest.ThreadupdateTime = time.Now()
			dialedConn, err := net.Dial("ip4:icmp", "0.0.0.0")
			if err != nil {
				client.Logging.Errorf("pinger:%v destination:%v, cannot create ipv4:icmp listen", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination())
				return
			}
			pingdest.ThreadupdateTime = time.Now()
			localAddr := dialedConn.LocalAddr()
			dialedConn.Close()
			pingdest.ThreadupdateTime = time.Now()
			//we sould use local address instead "0.0.0.0"
			netConn, err := net.ListenPacket("ip4:icmp", "0.0.0.0")
			if err != nil {
				client.Logging.Errorf("pinger:%v destination:%v, cannot create ipv4:icmp listen", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination())
				return
			}
			client.Logging.Debugf("pinger:%v destination:%v, localaddr:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), localAddr.String())

			conn, err := ipv4.NewRawConn(netConn)
			if err != nil {
				client.Logging.Errorf("pinger:%v destination:%v, cannot create ipv4:icmp conn", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination())
				return
			}
			defer conn.Close()
			pingdest.ThreadupdateTime = time.Now()
			seq++
			//Reset sequence for overflow
			if seq > 255 {
				seq = 1
			}
			//Chekck minimum size
			if pingdest.Object.GetPingdest().GetPacketSize() > 64 {
				packetSize = int(pingdest.Object.GetPingdest().GetPacketSize())
			} else {
				packetSize = 64
			}
			//Validate TTL
			if pingdest.Object.GetPingdest().GetTtl() > 0 && pingdest.Object.GetPingdest().GetTtl() < 255 {
				ttl = int(pingdest.Object.GetPingdest().GetTtl())
			} else {
				ttl = 30
			}
			body := &icmp.Echo{
				ID: pid, Seq: id<<8 | seq,
				Data: make([]byte, packetSize-42),
			}
			msg := &icmp.Message{
				Type: ipv4.ICMPTypeEcho,
				Code: 0,
				Body: body,
			}
			wb, err := msg.Marshal(nil)
			if err != nil {
				client.Logging.Errorf("pinger:%v destination:%v, cannot marshall icmp header:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), msg)
				pingdest.ThreadupdateTime = time.Now()
				continue
			}

			iph := &ipv4.Header{
				Version:  ipv4.Version,
				Len:      ipv4.HeaderLen,
				Protocol: ipv4.ICMPTypeEcho.Protocol(),
				TotalLen: ipv4.HeaderLen + len(wb),
				TTL:      int(ttl),
				Dst:      destination,
				Src:      net.ParseIP("0.0.0.0"),
				Flags:    ipv4.DontFragment,
			}
			//Set timeout to thread interval
			err = conn.SetDeadline(time.Now().Add(time.Duration(time.Millisecond * time.Duration(pingdest.Object.GetPingdest().GetInterval()))))
			if err != nil {
				client.Logging.Errorf("pinger:%v destination:%v, failed to set deadline/timeout err:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), err)
				continue
			}
			sentAt := time.Now()
			if err := conn.WriteTo(iph, wb, nil); err != nil {
				client.Logging.Errorf("pinger:%v destination:%v, unable to send ipv4:icmp packet err:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), err)
				continue
			}
			client.Logging.Tracef("pinger:%v destination:%v, sent ipv4:icmp seq:%v packet:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), id<<8|seq, iph)
			//Wait for return
			err = conn.SetDeadline(time.Now().Add(time.Duration(time.Millisecond * time.Duration(pingdest.Object.GetPingdest().GetInterval()))))
			if err != nil {
				client.Logging.Errorf("pinger:%v destination:%v, failed to set deadline, err: %v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), err)
				continue
			}
			b := make([]byte, 1500)
			ipv4h, p, _, err := conn.ReadFrom(b)
			copy(b, p)
			timeDiff := time.Now().Sub(sentAt).Milliseconds()
			pingdest.ThreadupdateTime = time.Now()
			if err != nil {
				client.Logging.Errorf("pinger:%v destination:%v, failed to receive ICMP packet err:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), err)
				stat := &proto.StatsObject{
					Client:    client.StatsClient,
					Timestamp: time.Now().UnixMicro(),
					Object: &proto.StatsObject_Pingstat{
						Pingstat: &proto.PingStat{
							Destination: pingdest.Object.GetPingdest().GetDestination(),
							Rtt:         int32(timeDiff),
							Code:        2,
							Ttl:         0,
							Error:       true,
						},
					},
				}
				select {
				case client.Statschannel <- stat:
					client.Logging.Tracef("pinger:%v destination:%v, sent stats:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), stat)
				default:
					client.Logging.Errorf("pinger:%v destination:%v, can not send stats:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), stat)
				}
				pingdest.ThreadupdateTime = time.Now()
				continue
			}
			var icmpMsg *icmp.Message
			if icmpMsg, err = icmp.ParseMessage(protocolICMP, b[:len(p)]); err != nil {
				client.Logging.Errorf("pinger:%v destination:%v, unable to parse ICMP message err:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), err)
				stat := &proto.StatsObject{
					Client:    client.StatsClient,
					Timestamp: time.Now().UnixMicro(),
					Object: &proto.StatsObject_Pingstat{
						Pingstat: &proto.PingStat{
							Destination: pingdest.Object.GetPingdest().GetDestination(),
							Rtt:         int32(timeDiff),
							Code:        2,
							Ttl:         int32(ipv4h.TTL),
							Error:       true,
						},
					},
				}
				select {
				case client.Statschannel <- stat:
					client.Logging.Tracef("pinger:%v destination:%v, sent stats:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), stat)
				default:
					client.Logging.Errorf("pinger:%v destination:%v, can not send stats:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), stat)
				}
				continue
			}
			switch pkt := icmpMsg.Body.(type) {
			case *icmp.Echo:
				sentseq := (id<<8 | (seq))
				revicedseq := pkt.Seq
				if revicedseq != sentseq {
					client.Logging.Errorf("pinger:%v destination:%v, expected seq:%v got:%v from the reply:%v, in:%v msec", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), sentseq, revicedseq, pkt, timeDiff)
					stat := &proto.StatsObject{
						Client:    client.StatsClient,
						Timestamp: time.Now().UnixMicro(),
						Object: &proto.StatsObject_Pingstat{
							Pingstat: &proto.PingStat{
								Destination: pingdest.Object.GetPingdest().GetDestination(),
								Rtt:         int32(timeDiff),
								Code:        2,
								Ttl:         int32(ipv4h.TTL),
								Error:       true,
							},
						},
					}
					select {
					case client.Statschannel <- stat:
						client.Logging.Tracef("pinger:%v destination:%v, sent stats:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), stat)
					default:
						client.Logging.Errorf("pinger:%v destination:%v, can not send stats:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), stat)
					}
				} else {
					client.Logging.Tracef("pinger:%v destination:%v, got the reply seq:%v packet:%v in:%v msec", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), pkt.Seq, pkt, timeDiff)
					stat := &proto.StatsObject{
						Client:    client.StatsClient,
						Timestamp: time.Now().UnixMicro(),
						Object: &proto.StatsObject_Pingstat{
							Pingstat: &proto.PingStat{
								Destination: pingdest.Object.GetPingdest().GetDestination(),
								Rtt:         int32(timeDiff),
								Code:        int32(icmpMsg.Code),
								Ttl:         int32(ipv4h.TTL),
								Error:       false,
							},
						},
					}
					select {
					case client.Statschannel <- stat:
						client.Logging.Tracef("pinger:%v destination:%v, sent stats:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), stat)
					default:
						client.Logging.Errorf("pinger:%v destination:%v, can not send stats:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), stat)
					}
				}
			case *icmp.TimeExceeded:
				client.Logging.Tracef("pinger:%v destination:%v, ttl:%v expired at:%v, in:%v msec", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), ttl, ipv4h.Src, timeDiff)
				stat := &proto.StatsObject{
					Client:    client.StatsClient,
					Timestamp: time.Now().UnixMicro(),
					Object: &proto.StatsObject_Pingstat{
						Pingstat: &proto.PingStat{
							Destination: pingdest.Object.GetPingdest().GetDestination(),
							Rtt:         int32(timeDiff),
							Code:        11,
							Ttl:         int32(ipv4h.TTL),
							Error:       true,
						},
					},
				}
				select {
				case client.Statschannel <- stat:
					client.Logging.Tracef("pinger:%v destination:%v, sent stats:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), stat)
				default:
					client.Logging.Errorf("pinger:%v destination:%v, can not send stats:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), stat)
				}
			default:
				client.Logging.Warnf("pinger:%v destination:%v, unhandled packet:%v in:%v msec", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), icmpMsg, timeDiff)
				stat := &proto.StatsObject{
					Client:    client.StatsClient,
					Timestamp: time.Now().UnixMicro(),
					Object: &proto.StatsObject_Pingstat{
						Pingstat: &proto.PingStat{
							Destination: pingdest.Object.GetPingdest().GetDestination(),
							Rtt:         int32(timeDiff),
							Code:        int32(icmpMsg.Code),
							Ttl:         int32(ipv4h.TTL),
							Error:       true,
						},
					},
				}
				select {
				case client.Statschannel <- stat:
					client.Logging.Tracef("pinger:%v destination:%v, sent stats:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), stat)
				default:
					client.Logging.Errorf("pinger:%v destination:%v, can not send stats:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), stat)
				}
			}
			if int32(timeDiff) > pingdest.Object.GetPingdest().GetInterval() {
				client.Logging.Errorf("pinger:%v destination:%v, reponse time:%v can not be bigger than interval:%v which is also timeout of the request", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), timeDiff, pingdest.Object.GetPingdest().GetInterval())
			}
			pingdest.ThreadupdateTime = time.Now()
			netConn.Close()
		}
	}
	client.Logging.Infof("pinger:%v destination:%v, exiting", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), pingdest.Object.GetPingdest())
}

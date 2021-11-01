package client

import (
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
	- Waits for the reply till the next interval, sends error stats in case of context timeout
TODO:
    - Checking sequence (send and recived packet integrity)
*/
func CheckIcmpPing(pingdest *MonObject, client *XmonClient) {
	pingdest.ThreadupdateTime = time.Now()
	client.Logging.Infof("pinger:%v destination:%v, initial values:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), pingdest.Object.GetPingdest())
	pid := os.Getpid() & 0xffff
	hop := 254
	seq := 0
	pingdest.ThreadupdateTime = time.Now()
	packetSize := 64
	ttl := 0
	exit := false
	var destination net.IP
	for !exit {
		select {
		case <-pingdest.Notify:
			client.Logging.Debugf("pinger:%v received stop request", pingdest.Object.GetPingdest().GetName())
			exit = true
		default:
			//As we are lots of error handling which can be changed during continuous ping process we MUST wait before looping. If not we MAY hit the same error in very short time:)
			pingdest.ThreadInterval = pingdest.Object.GetPingdest().GetInterval()
			time.Sleep(time.Duration(time.Duration(pingdest.ThreadInterval) * time.Millisecond))
			pingdest.ThreadupdateTime = time.Now()
			netConn, err := net.ListenPacket("ip4:icmp", "0.0.0.0")
			if err != nil {
				client.Logging.Errorf("pinger:%v destination:%v, cannot create ipv4:icmp listen", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination())
				continue
			}
			defer netConn.Close()
			conn, err := ipv4.NewRawConn(netConn)
			if err != nil {
				client.Logging.Errorf("pinger:%v destination:%v, cannot create ipv4:icmp conn", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination())
				continue
			}
			seq++
			//Reset sequence for overflow
			if seq > 255 {
				seq = 1
			}
			//Chekck minimum size
			if pingdest.Object.GetPingdest().GetPacketSize() > 64 {
				packetSize = int(pingdest.Object.GetPingdest().GetPacketSize())
			}
			//Validate TTL
			if pingdest.Object.GetPingdest().GetTtl() > 0 && pingdest.Object.GetPingdest().GetTtl() < 255 {
				ttl = int(pingdest.Object.GetPingdest().GetTtl())
			}
			body := &icmp.Echo{
				ID: pid, Seq: hop<<8 | seq,
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
				continue
			}
			destination = net.ParseIP(pingdest.Object.GetPingdest().GetDestination())
			if destination != nil {
				if len(destination.To4()) != net.IPv4len {
					client.Logging.Errorf("pinger:%v destination:%v, unimplemented destiantion type:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), destination)
					continue
				}
			} else {
				ips, err := net.LookupHost(pingdest.Object.GetPingdest().GetDestination())
				if err == nil {
					destination = net.ParseIP(ips[0])
					client.Logging.Tracef("pinger:%v destination:%v, resolved as:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), destination)
				} else {
					client.Logging.Errorf("pinger:%v destination:%v, resolve error for destination", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination())
					continue
				}
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
			sentAt := time.Now()
			if err := conn.WriteTo(iph, wb, nil); err != nil {
				client.Logging.Errorf("pinger:%v destination:%v, unable to send ipv4:icmp packet err:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), err)
				continue
			}
			client.Logging.Tracef("pinger:%v destination:%v, sent ipv4:icmp seq:%v packet:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), hop<<8|seq, iph)
			//Wait for return
			err = conn.SetDeadline(time.Now().Add(time.Duration(time.Millisecond * time.Duration(pingdest.Object.GetPingdest().GetInterval()))))
			if err != nil {
				client.Logging.Errorf("pinger:%v destination:%v, failed to set deadline, err: %v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), err)
				continue
			}
			b := make([]byte, 1500)
			ipv4h, p, _, err := conn.ReadFrom(b)
			copy(b, p)
			if err != nil {
				client.Logging.Errorf("pinger:%v destination:%v, failed to receive ICMP packet err:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), err)
				continue
			}
			timeDiff := time.Now().Sub(sentAt).Milliseconds()
			var icmpMsg *icmp.Message
			if icmpMsg, err = icmp.ParseMessage(protocolICMP, b[:len(p)]); err != nil {
				client.Logging.Errorf("pinger:%v destination:%v, unable to parse ICMP message err:%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), err)
				continue
			}
			switch pkt := icmpMsg.Body.(type) {
			case *icmp.Echo:
				if pkt.Seq != hop<<8|seq {
					client.Logging.Errorf("pinger:%v destination:%v, expected seq:%v got:%v from the reply:%v, in:%v msec", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), hop<<8|seq, pkt.Seq, pkt, timeDiff)
					continue
				}
				client.Logging.Tracef("pinger:%v destination:%v, got the reply seq:%v packet:%v in:%v msec", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), pkt.Seq, pkt, timeDiff)
				stat := &proto.StatsObject{
					Client:    client.StatsClient,
					Timestamp: time.Now().UnixNano(),
					Object: &proto.StatsObject_Pingstat{
						Pingstat: &proto.PingStat{
							Destination: pingdest.Object.GetPingdest().GetDestination(),
							Rtt:         int32(timeDiff),
							Code:        int32(icmpMsg.Code),
							Ttl:         int32(ipv4h.TTL),
						},
					},
				}
				select {
				case client.Statschannel <- stat:
					client.Logging.Debugf("pinger:%v destination:%v, can not send stats%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), stat)
				default:
					client.Logging.Errorf("pinger:%v destination:%v, can not send stats%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), stat)
				}
			case *icmp.TimeExceeded:
				client.Logging.Tracef("pinger:%v destination:%v, ttl:%v expired at:%v, in:%v msec", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), ttl, ipv4h.Src, timeDiff)
				stat := &proto.StatsObject{
					Client:    client.StatsClient,
					Timestamp: time.Now().UnixNano(),
					Object: &proto.StatsObject_Pingstat{
						Pingstat: &proto.PingStat{
							Destination: pingdest.Object.GetPingdest().GetDestination(),
							Rtt:         int32(timeDiff),
							Code:        int32(icmpMsg.Code),
							Ttl:         int32(ipv4h.TTL),
						},
					},
				}
				select {
				case client.Statschannel <- stat:
					client.Logging.Debugf("pinger:%v destination:%v, can not send stats%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), stat)
				default:
					client.Logging.Errorf("pinger:%v destination:%v, can not send stats%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), stat)
				}
			default:
				client.Logging.Warnf("pinger:%v destination:%v, unhandled packet:%v in:%v msec", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), icmpMsg, timeDiff)
				stat := &proto.StatsObject{
					Client:    client.StatsClient,
					Timestamp: time.Now().UnixNano(),
					Object: &proto.StatsObject_Pingstat{
						Pingstat: &proto.PingStat{
							Destination: pingdest.Object.GetPingdest().GetDestination(),
							Rtt:         int32(timeDiff),
							Code:        int32(icmpMsg.Code),
							Ttl:         int32(ipv4h.TTL),
						},
					},
				}
				select {
				case client.Statschannel <- stat:
					client.Logging.Debugf("pinger:%v destination:%v, can not send stats%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), stat)
				default:
					client.Logging.Errorf("pinger:%v destination:%v, can not send stats%v", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), stat)
				}
			}
			if int32(timeDiff) > pingdest.Object.GetPingdest().GetInterval() {
				client.Logging.Errorf("pinger:%v destination:%v, reponse time:%v can not be bigger than interval:%v which is also timeout of the request", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), timeDiff, pingdest.Object.GetPingdest().GetInterval())
			}
			pingdest.ThreadupdateTime = time.Now()
		}
	}
	client.Logging.Infof("pinger:%v destination:%v, exiting", pingdest.Object.GetPingdest().GetName(), pingdest.Object.GetPingdest().GetDestination(), pingdest.Object.GetPingdest())
}

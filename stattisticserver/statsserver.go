package main

/* ==> Caveats
When run from `brew services`, `prometheus` is run from
`prometheus_brew_services` and uses the flags in:
   /usr/local/etc/prometheus.args

To have launchd start prometheus now and restart at login:
  brew services start prometheus
Or, if you don't want/need a background service you can just run:
  prometheus --config.file=/usr/local/etc/prometheus.yml
==> Summary
🍺  /usr/local/Cellar/prometheus/2.28.1: 21 files, 177.2MB */
import (
	"encoding/binary"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	proto "github.com/denizaydin/xmon/proto"
	influxdb2 "github.com/influxdata/influxdb-client-go"
	ripe "github.com/mehrdadrad/mylg/ripe"

	logrus "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
)

var (
	//server - holds the statsserver variables
	statsserver *StatsServer
)

//Stat - Holds varibles of the received stat
type Stat struct {
	ClientName     string
	ClientAS       string
	ClientASHolder string
	ClientNet      string
	HopASN         string
	HopASNowner    string
	NmonStat       *proto.StatsObject
}

//StatsServer - Holds varibles of the server
type StatsServer struct {
	logging *logrus.Logger
	//StatsGRPCServerAddr - holds grpc server net address
	serverAddress string
	//influxAddress - holds prometheous metrics server net address
	influxAddress string
	//influxToken string
	influxToken string
	//dbwriteChannel
	dbwriteChannel chan *Stat
	//ip2ASN - Holds ip to asn mappings for caching
	ip2ASN map[string]float64
	//ip2Holder - holds ip to as owner, holder mapping for caching
	ip2Holder map[string]string
}

func init() {
	//Create new server instance
	statsserver = &StatsServer{
		logging:        &logrus.Logger{},
		serverAddress:  "",
		influxAddress:  "",
		influxToken:    "",
		dbwriteChannel: make(chan *Stat, 1000),
		ip2ASN:         map[string]float64{},
		ip2Holder:      map[string]string{},
	}
	statsserver.logging = logrus.New()
	statsserver.logging.SetFormatter(&logrus.TextFormatter{
		DisableColors: false,
		FullTimestamp: true,
	})
	statsserver.logging.SetOutput(os.Stdout)
	logLevel := "info"
	flag.StringVar(&logLevel, "loglevel", "disable", "disable,info, error, warning,debug or trace")
	flag.StringVar(&statsserver.serverAddress, "address", "localhost:8081", "server address")
	flag.StringVar(&statsserver.influxAddress, "influxaddr", "http://localhost:8086", "server address")
	flag.StringVar(&statsserver.influxToken, "influxtoken", "AhzrzUydroQUHT6r_A11yS_x0hDG_S7zsrHB8LwyFr1VRjD4Y5g_66dBKk3T5lPnHTu3VC7PPB4PCvTTu_2I6Q==", "server token")

	flag.Parse()
	switch logLevel {
	case "disable":
		statsserver.logging.SetOutput(ioutil.Discard)
	case "info":
		statsserver.logging.SetLevel(logrus.InfoLevel)
	case "error":
		statsserver.logging.SetLevel(logrus.ErrorLevel)
	case "warn":
		statsserver.logging.SetLevel(logrus.WarnLevel)
	case "debug":
		statsserver.logging.SetLevel(logrus.DebugLevel)
	case "trace":
		statsserver.logging.SetLevel(logrus.TraceLevel)
	default:
		statsserver.logging.SetLevel(logrus.DebugLevel)
	}
	statsserver.ip2ASN = make(map[string]float64)
	statsserver.ip2Holder = make(map[string]string)
	statsserver.logging.Info("statisticserver: started with paramaters:%v", statsserver)
}

//IP2long - returng ip address as float64 for prometheus metric recording
func IP2long(ipstr string) float64 {
	ip := net.ParseIP(ipstr)
	if ip == nil {
		return 0
	}
	ip = ip.To4()
	return float64(binary.BigEndian.Uint32(ip))
}

//RecordStats - Client streaming process, clients are send stats with this method.
//Recives client statistic
func (s *StatsServer) RecordStats(stream proto.Stats_RecordStatsServer) error {
	//initial values
	var err error
	clientIP := "127.0.0.1:1"
	clientAs := "unknown"
	clientHolder := "unknown"
	clientNet := clientIP + "/24"
	pr, ok := peer.FromContext(stream.Context())
	if ok {
		clientIP = strings.Split(pr.Addr.String(), ":")[0]
	}
	s.logging.Debugf("xmon statisticserver: statistic request from the address:%v", clientIP)
	_, ipnet, neterr := net.ParseCIDR(clientIP + "/24")
	if neterr == nil {
		clientNet = ipnet.String()
	}
	s.logging.Infof("xmon statisticserver: new client stats from ip:%v network:%v", clientIP, clientNet)
	if net.ParseIP(clientIP).IsGlobalUnicast() && !net.ParseIP(clientIP).IsPrivate() {
		_, ok = s.ip2ASN[clientNet]
		if !ok {
			s.logging.Debugf("xmon statisticserver: retriving the client:%v information from ripe", clientIP)
			var p ripe.Prefix
			p.Set(clientNet)
			p.GetData()
			s.logging.Debugf("xmon statisticserver: got the client information from ripe:%v", p)
			data, _ := p.Data["data"].(map[string]interface{})
			asns := data["asns"].([]interface{})
			//TODO: more than one ASN per prefix?
			for _, h := range asns {
				s.ip2Holder[clientNet] = h.(map[string]interface{})["holder"].(string)
				s.ip2ASN[clientNet] = h.(map[string]interface{})["asn"].(float64)
			}
		}
		clientAs = fmt.Sprintf("%f", s.ip2ASN[clientNet])
		clientHolder = s.ip2Holder[clientNet]
	}

	for {
		receivedstat, err := stream.Recv()
		if err == nil {
			var stat = &Stat{
				ClientName:     receivedstat.GetClient().GetName(),
				ClientAS:       clientAs,
				ClientASHolder: clientHolder,
				ClientNet:      clientNet,
				NmonStat:       receivedstat,
			}
			switch t := stat.NmonStat.Object.(type) {
			case *proto.StatsObject_Tracestat:
				s.logging.Debugf("xmon statisticserver: received %v stat:%v request from:%v", t, stat, pr)
				hopIP := stat.NmonStat.GetTracestat().GetHopIP()
				hopASN := "unknown"
				hopASNHolder := "unknown"
				hopNet := hopIP + "/24"
				s.logging.Debugf("xmon statisticserver: hop ip for the address:%v", hopIP)
				_, ipnet, neterr := net.ParseCIDR(hopIP + "/24")
				if neterr == nil {
					hopNet = ipnet.String()
				}
				s.logging.Infof("xmon statisticserver: hop ip for the address:%v network:%v", hopIP, hopNet)
				if net.ParseIP(hopIP).IsGlobalUnicast() && !net.ParseIP(hopIP).IsPrivate() {
					_, ok = s.ip2ASN[hopNet]
					if !ok {
						s.logging.Debugf("xmon statisticserver: retriving the hop:%v information from ripe", hopIP)
						var p ripe.Prefix
						p.Set(hopNet)
						p.GetData()
						s.logging.Debugf("xmon statisticserver: got the hop information from ripe:%v", p)
						data, _ := p.Data["data"].(map[string]interface{})
						asns := data["asns"].([]interface{})
						//TODO: more than one ASN per prefix?
						for _, h := range asns {
							s.ip2Holder[hopNet] = h.(map[string]interface{})["holder"].(string)
							s.ip2ASN[hopNet] = h.(map[string]interface{})["asn"].(float64)
						}
					}
					hopASN = fmt.Sprintf("%f", s.ip2ASN[hopNet])
					hopASNHolder = s.ip2Holder[hopNet]
				}
				stat.HopASN = hopASN
				stat.HopASNowner = hopASNHolder
				if cap(s.dbwriteChannel) > len(s.dbwriteChannel) {
					s.dbwriteChannel <- stat
				} else {
					s.logging.Errorf("xmon statisticserver: db write channel is full, cap:%v current length:%v", cap(s.dbwriteChannel), len(s.dbwriteChannel))
				}
			default:
				s.logging.Debugf("xmon statisticserver: received %v stat:%v request from:%v", t, stat, pr)
			}
			if cap(s.dbwriteChannel) > len(s.dbwriteChannel) {
				s.dbwriteChannel <- stat
				s.logging.Tracef("xmon statisticserver: db write channel, cap:%v current length:%v", cap(s.dbwriteChannel), len(s.dbwriteChannel))
			} else {
				s.logging.Errorf("xmon statisticserver: db write channel is full, cap:%v current length:%v", cap(s.dbwriteChannel), len(s.dbwriteChannel))
			}
		} else {
			s.logging.Errorf("xmon statisticserver: during reading client stat request from:%v", pr, err)
			break
		}
	}
	return err
}
func writeToDB(s *StatsServer) {
	client := influxdb2.NewClient(s.influxAddress, s.influxToken)
	// user blocking write client for writes to desired bucket
	// Get non-blocking write client
	writeAPI := client.WriteAPI("my-org", "xmon")
	for {
		select {
		case stat := <-s.dbwriteChannel:
			switch t := stat.NmonStat.Object.(type) {
			case *proto.StatsObject_Clientstat:
				s.logging.Tracef("xmon statisticserver: dbclient received %v stat:%v", t, stat)
				p := influxdb2.NewPoint(
					"clientstat",
					map[string]string{
						"client_name":   stat.ClientName,
						"client_as":     stat.ClientAS,
						"clietn_asname": stat.ClientASHolder,
					},
					map[string]interface{}{
						"configured_objects": stat.NmonStat.GetClientstat().GetConfiguredMonObjects(),
						"running_objects":    stat.NmonStat.GetClientstat().GetRunningMonObjects(),
					},
					time.Now())
				// write asynchronously
				writeAPI.WritePoint(p)
				// write asynchronously
			case *proto.StatsObject_Pingstat:
				s.logging.Tracef("xmon statisticserver: dbclient received %v stat:%v", t, stat)
				p := influxdb2.NewPoint(
					"pingstat",
					map[string]string{
						"client_name":   stat.ClientName,
						"client_as":     stat.ClientAS,
						"clietn_asname": stat.ClientASHolder,
						"destination":   stat.NmonStat.GetPingstat().GetDestination(),
					},
					map[string]interface{}{
						"rtt":  stat.NmonStat.GetPingstat().GetRtt(),
						"ttl":  stat.NmonStat.GetPingstat().GetTtl(),
						"code": stat.NmonStat.GetPingstat().GetCode(),
					},
					time.Now())
				// write asynchronously
				writeAPI.WritePoint(p)
			case *proto.StatsObject_Resolvestat:
				s.logging.Tracef("xmon statisticserver: dbclient received %v stat:%v", t, stat)
				p := influxdb2.NewPoint(
					"resolvestat",
					map[string]string{
						"client_name":   stat.ClientName,
						"client_as":     stat.ClientAS,
						"clietn_asname": stat.ClientASHolder,
						"destination":   stat.NmonStat.GetResolvestat().GetDestination(),
						"resolver":      stat.NmonStat.GetResolvestat().GetResolver(),
					},
					map[string]interface{}{
						"rtt":        stat.NmonStat.GetResolvestat().GetRtt(),
						"resolvedip": stat.NmonStat.GetResolvestat().GetResolvedip(),
					},
					time.Now())
				// write asynchronously
				writeAPI.WritePoint(p)
			case *proto.StatsObject_Tracestat:
				s.logging.Tracef("xmon statisticserver: dbclient received %v stat:%v", t, stat)
				p := influxdb2.NewPoint(
					"tracestat",
					map[string]string{
						"client_name":   stat.ClientName,
						"client_as":     stat.ClientAS,
						"clietn_asname": stat.ClientASHolder,
						"destination":   stat.NmonStat.GetTracestat().GetDestination(),
						"hop_ip":        stat.NmonStat.GetTracestat().GetHopIP(),
						"hop_asn":       stat.HopASN,
						"hop_owner":     stat.HopASNowner,
					},
					map[string]interface{}{
						"rtt": stat.NmonStat.GetTracestat().GetHopTTL(),
						"ttl": stat.NmonStat.GetTracestat().GetHopTTL(),
					},
					time.Now())
				// write asynchronously
				writeAPI.WritePoint(p)
			case *proto.StatsObject_Appstat:
				s.logging.Tracef("xmon statisticserver: dbclient received %v stat:%v", t, stat)
				p := influxdb2.NewPoint(
					"appstat",
					map[string]string{
						"client_name":   stat.ClientName,
						"client_as":     stat.ClientAS,
						"clietn_asname": stat.ClientASHolder,
						"destination":   stat.NmonStat.GetAppstat().Destination,
					},
					map[string]interface{}{
						"client_Network_Delay":  stat.NmonStat.GetAppstat().ClientNetworkDelay,
						"server_Network_Delay":  stat.NmonStat.GetAppstat().ServerNetworkDelay,
						"server_Response_Delay": stat.NmonStat.GetAppstat().ServerResponseDelay,
					},
					time.Now())
				// write asynchronously
				writeAPI.WritePoint(p)
			}
		}
	}
	/* Force all unwritten data to be sent
	writeAPI.Flush()
	// Ensures background processes finishes
	client.Close()
	*/
}
func main() {
	statsserver.logging.Infof("xmon statisticserver: server is initialized with parameters:%v", statsserver)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs)
	go func() {
		s := <-sigs
		switch s {
		case syscall.SIGURG:
			statsserver.logging.Infof("xmon statisticserver: received unhandled %v signal from os:", s)
		default:
			statsserver.logging.Infof("xmon statisticserver: received %v signal from os,exiting", s)
			os.Exit(1)
		}
	}()
	go writeToDB(statsserver)
	grpcServer := grpc.NewServer(grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
		MinTime:             5 * time.Second, // If a client pings more than once every x seconds, terminate the connection
		PermitWithoutStream: true,            // Allow pings even when there are no active streams
	}), grpc.KeepaliveParams(keepalive.ServerParameters{
		MaxConnectionIdle: 15 * time.Second, // If a client is idle for x seconds, send a GOAWAY
		Time:              5 * time.Second,  // Ping the client if it is idle for x seconds to ensure the connection is still active
		Timeout:           5 * time.Second,  // Wait x second for the ping ack before assuming the connection is dead
	}))
	statsserver.logging.Debugf("xmon statisticserver: starting server at:%v", statsserver.serverAddress)
	listener, err := net.Listen("tcp", statsserver.serverAddress)
	if err != nil {
		statsserver.logging.Fatalf("xmon statisticserver: error creating the server %v", err)
	}
	proto.RegisterStatsServer(grpcServer, statsserver)
	statsserver.logging.Infof("xmon statisticserver: grpc started server at:%v", listener.Addr())
	grpcServer.Serve(listener)
}

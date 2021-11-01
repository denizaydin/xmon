package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"io/ioutil"
	"os"
	"strings"
	"sync"
	"time"

	xmonClient "github.com/denizaydin/xmon/client"
	proto "github.com/denizaydin/xmon/proto"
	logrus "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

// configServer: Configuration Server that will push monitoring information
var configServer *string

// statsServer: Statistic reporting server that we will send monitoring results
var statsServer *string

// willingTobeMonitored: Do we want to be monitored by other clients?
var willingTobeMonitored bool

// clientName
var clientName string

// clientGroups
var groups *string

//client: New broadcast client for configuration
var configclient proto.ConfigServerClient

//wait: Global wail group for control
var wait *sync.WaitGroup

//conn: Current GRPC connection to the server
var configconn *grpc.ClientConn

//client - Pointer of the current client
var c *xmonClient.XmonClient

var logging *logrus.Logger

func init() {
	c = &xmonClient.XmonClient{
		ConfigClient:            &proto.Client{},
		IsConfigClientConnected: false,
		StatsClient:             &proto.Client{},
		IsStatsClientConnected:  false,
		Statschannel:            make(chan *proto.StatsObject, 100),
		MonObecjts:              map[string]*xmonClient.MonObject{},
		MonObjectScanTimer:      &time.Ticker{},
		Logging:                 &logrus.Logger{},
		WaitChannel:             make(chan int),
		WaitGroup:               wait,
	}
	// Log as JSON instead of the default ASCII formatter.
	c.Logging = logrus.New()
	c.Logging.SetFormatter(&logrus.TextFormatter{
		DisableColors: false,
		FullTimestamp: true,
	})
	// Output to stdout instead of the default stderr
	// Can be any io.Writer, see below for File example
	c.Logging.SetOutput(os.Stdout)
	logLevel := "info"
	configServer = flag.String("configServer", "127.0.0.1:8080", "current environment")
	statsServer = flag.String("statsServer", "127.0.0.1:8081", "port number")
	flag.StringVar(&logLevel, "loglevel", "disable", "disable, info, error, warning,debug or trace")
	flag.BoolVar(&c.ConfigClient.AddAsPingDest, "isPingDest", false, "willing to be pinged")
	flag.BoolVar(&c.ConfigClient.AddAsTraceDest, "isTraceDest", false, "willing to be traced")
	flag.BoolVar(&c.ConfigClient.AddAsAppDest, "isAppDest", false, "willing to be monitored by app, not implemented")
	flag.StringVar(&c.ConfigClient.Name, "clientName", "", "name to be used as identifier on the server, operating system name will be used as a default")
	groups = flag.String("groups", "default", "client groups separeted by comma")
	flag.Parse()
	switch logLevel {
	case "disable":
		c.Logging.SetOutput(ioutil.Discard)
	case "info":
		c.Logging.SetLevel(logrus.InfoLevel)
	case "error":
		c.Logging.SetLevel(logrus.ErrorLevel)
	case "warn":
		c.Logging.SetLevel(logrus.WarnLevel)
	case "debug":
		c.Logging.SetLevel(logrus.DebugLevel)
	case "trace":
		c.Logging.SetLevel(logrus.TraceLevel)
	default:
		c.Logging.SetLevel(logrus.DebugLevel)
	}

	if c.ConfigClient.Name == "" {
		hostname, err := os.Hostname()
		if err != nil {
			c.Logging.Fatalf("client name is required and we can not get the hostname")
		} else {
			c.Logging.Warnf("no client name is given, setting clientname to hostname:%v", hostname)
			c.ConfigClient.Name = hostname
		}
	}
	clientGroups := make(map[string]string)
	for _, pair := range strings.Split(*groups, ",") {
		clientGroups[pair] = pair
	}
	id := sha256.Sum256([]byte(time.Now().String() + clientName))
	c.ConfigClient = &proto.Client{
		Id:             hex.EncodeToString(id[:]),
		Name:           c.ConfigClient.Name,
		Groups:         clientGroups,
		AddAsPingDest:  c.ConfigClient.AddAsPingDest,
		AddAsTraceDest: c.ConfigClient.AddAsTraceDest,
		AddAsAppDest:   c.ConfigClient.AddAsAppDest,
	}
	c.StatsClient = &proto.Client{
		Id:     hex.EncodeToString(id[:]),
		Name:   c.ConfigClient.Name,
		Groups: clientGroups,
	}
	c.WaitGroup = &sync.WaitGroup{}
	c.Logging.Info("initilazed monitoring client service")
	c.Logging.Debugf("client parameters are:%v", c)
}

//getMonitoringObjects - retrives monitoring object from a GRPC server
func getMonitoringObjects(client *xmonClient.XmonClient) {
	c.Logging.Tracef("retriving configuration from:%v", configServer)
	for {
		if !c.IsConfigClientConnected {
			c.Logging.Infof("tring to connect config server:%v with name:%v and id:%v", *configServer, c.ConfigClient.GetName(), c.ConfigClient.GetId())
			configconn, err := grpc.Dial(*configServer, grpc.WithInsecure(), grpc.WithKeepaliveParams(keepalive.ClientParameters{
				Time:                1 * time.Second, // send pings every 10 seconds if there is no activity
				Timeout:             time.Second,     // wait 1 second for ping ack before considering the connection dead
				PermitWithoutStream: true,            // send pings even without active streams
			}), grpc.WithDefaultServiceConfig(`{
				"methodConfig": [{
				  "name": [{"service": "nmon client service"}],
				  "waitForReady": true,
				  "retryPolicy": {
					  "MaxAttempts": 4,
					  "InitialBackoff": "1s",
					  "MaxBackoff": "6s",
					  "BackoffMultiplier": 1.5,
					  "RetryableStatusCodes": [ "UNAVAILABLE" ]
				  }
				}]}`), grpc.WithBlock())
			if err != nil {
				c.Logging.Errorf("could not connect to config service: %v, waiting for 10sec to retry", err)
				time.Sleep(10 * time.Second)
				break
			}
			c.Logging.Debug("connected to the configuration server, registering")
			configclient = proto.NewConfigServerClient(configconn)
			stream, err := configclient.CreateStream(context.Background(), &proto.Connect{
				Client: c.ConfigClient,
			})
			if err != nil {
				c.Logging.Errorf("configuration service registration failed: %v, waiting for 10sec to retry", err)
				time.Sleep(10 * time.Second)
				break
			}
			c.IsConfigClientConnected = true
			c.Logging.Info("configuration service is registered, waiting for monitoring object to be streamed")
			for {
				monitoringObject, err := stream.Recv()
				if err != nil {
					c.Logging.Errorf("error reading configuration message: %v", err)
					c.IsConfigClientConnected = false
					break
				}
				c.Logging.Debugf("received configuration message %s", monitoringObject)
				// adding configuration objects into conf objects map. As same destinstion can be added for multiple type, uniquness is needed for the map key.
				switch t := monitoringObject.Object.(type) {
				case *proto.MonitoringObject_Pingdest:
					c.MonObecjts[monitoringObject.GetPingdest().GetName()+"-ping"] = &xmonClient.MonObject{
						ConfigurationUpdatetime: time.Now(),
						Object:                  monitoringObject,
					}
					// Check interval
					if c.MonObecjts[monitoringObject.GetPingdest().GetName()+"-ping"].Object.GetPingdest().Interval < 100 {
						c.Logging.Warnf("%v, interval for ping object:%v is too low", monitoringObject.GetPingdest().GetName(), c.MonObecjts[monitoringObject.GetPingdest().GetName()+"-ping"].Object.GetPingdest().Interval)
						c.MonObecjts[monitoringObject.GetPingdest().GetName()+"-ping"].Object.GetPingdest().Interval = 100
					} else if c.MonObecjts[monitoringObject.GetPingdest().GetName()+"-ping"].Object.GetPingdest().Interval > 60000 {
						c.Logging.Warnf("%v, interval for ping object:%v is too high", monitoringObject.GetPingdest().GetName(), c.MonObecjts[monitoringObject.GetPingdest().GetName()+"-ping"].Object.GetPingdest().Interval)
						c.MonObecjts[monitoringObject.GetPingdest().GetName()+"-ping"].Object.GetPingdest().Interval = 60000
					}
				case *proto.MonitoringObject_Resolvedest:
					c.MonObecjts[monitoringObject.GetResolvedest().GetName()+"-resolve"] = &xmonClient.MonObject{
						ConfigurationUpdatetime: time.Now(),
						Object:                  monitoringObject,
					}
					// Configuration checks
					// Check interval
					if c.MonObecjts[monitoringObject.GetResolvedest().GetName()+"-resolve"].Object.GetResolvedest().Interval < 3000 {
						c.MonObecjts[monitoringObject.GetResolvedest().GetName()+"-resolve"].Object.GetResolvedest().Interval = 3000
						c.Logging.Warnf("%v, interval for resolve object:%v is too low", monitoringObject.GetResolvedest().GetName(), c.MonObecjts[monitoringObject.GetResolvedest().GetName()+"-resolve"].Object.GetResolvedest().Interval)
					} else if c.MonObecjts[monitoringObject.GetResolvedest().GetName()+"-resolve"].Object.GetResolvedest().Interval > 60000 {
						c.Logging.Warnf("%v, interval for resolve object:%v is too high", monitoringObject.GetResolvedest().GetName(), c.MonObecjts[monitoringObject.GetResolvedest().GetName()+"-resolve"].Object.GetResolvedest().Interval)
						c.MonObecjts[monitoringObject.GetResolvedest().GetName()+"-resolve"].Object.GetResolvedest().Interval = 60000
					}
				case *proto.MonitoringObject_Tracedest:
					c.MonObecjts[monitoringObject.GetTracedest().GetName()+"-trace"] = &xmonClient.MonObject{
						ConfigurationUpdatetime: time.Now(),
						Object:                  monitoringObject,
					}
					// Configuration checks
					// Check interval
					if c.MonObecjts[monitoringObject.GetTracedest().GetName()+"-trace"].Object.GetTracedest().Interval < 60000 {
						c.MonObecjts[monitoringObject.GetTracedest().GetName()+"-trace"].Object.GetTracedest().Interval = 60000
						c.Logging.Warnf("%v, interval for trace object:%v is too low", monitoringObject.GetTracedest().GetName(), c.MonObecjts[monitoringObject.GetTracedest().GetName()+"-trace"].Object.GetTracedest().Interval)
					} else if c.MonObecjts[monitoringObject.GetTracedest().GetName()+"-trace"].Object.GetTracedest().Interval > 1800000 {
						c.Logging.Warnf("%v, interval for trace object:%v is too high", monitoringObject.GetTracedest().GetName(), c.MonObecjts[monitoringObject.GetTracedest().GetName()+"-trace"].Object.GetTracedest().Interval)
						c.MonObecjts[monitoringObject.GetTracedest().GetName()+"-trace"].Object.GetTracedest().Interval = 1800000
					}
				case *proto.MonitoringObject_Appdest:
					c.MonObecjts[monitoringObject.GetAppdest().GetName()+"-app"] = &xmonClient.MonObject{
						ConfigurationUpdatetime: time.Now(),
						Object:                  monitoringObject,
					}
					// Check interval
					if c.MonObecjts[monitoringObject.GetAppdest().GetName()+"-app"].Object.GetAppdest().Interval < 100 {
						c.Logging.Warnf("%v, interval for app object:%v is too low", monitoringObject.GetAppdest().GetName(), c.MonObecjts[monitoringObject.GetAppdest().GetName()+"-app"].Object.GetAppdest().Interval)
						c.MonObecjts[monitoringObject.GetAppdest().GetName()+"-app"].Object.GetAppdest().Interval = 100
					} else if c.MonObecjts[monitoringObject.GetAppdest().GetName()+"-app"].Object.GetAppdest().Interval > 60000 {
						c.Logging.Warnf("%v, interval for app object:%v is too high", monitoringObject.GetAppdest().GetName(), c.MonObecjts[monitoringObject.GetAppdest().GetName()+"-app"].Object.GetAppdest().Interval)
						c.MonObecjts[monitoringObject.GetAppdest().GetName()+"-app"].Object.GetAppdest().Interval = 60000
					}
				case nil:
					// The field is not set.
				default:
					c.Logging.Errorf("unexpected monitoring object type %T", t)
				}
			}

		}
		c.Logging.Errorf("configuration service failed, waiting for 3sec to retry")
		time.Sleep(1 * time.Second)
	}
}
func connectStatsServer(client *xmonClient.XmonClient) {
	c.Logging.Infof("started statistic server connection:%v", statsServer)
	go func() {
		for {
			c.Logging.Infof("is statistic server connected?:%v", c.IsStatsClientConnected)
			if !c.IsStatsClientConnected {
				c.Logging.Debugf("tring to connect statistic server:%v with name:%v and id:%v", *statsServer, c.StatsClient.GetName(), c.StatsClient.GetId())
				conn, err := grpc.Dial(*statsServer, grpc.WithInsecure(), grpc.WithKeepaliveParams(keepalive.ClientParameters{
					Time:                3 * time.Second, // send pings every 10 seconds if there is no activity
					Timeout:             5 * time.Second, // wait 1 second for ping ack before considering the connection dead
					PermitWithoutStream: true,            // send pings even without active streams
				}), grpc.WithDefaultServiceConfig(`{
			"methodConfig": [{
			  "name": [{"service": "nmon client service"}],
			  "waitForReady": true,
			  "retryPolicy": {
				  "MaxAttempts": 4,
				  "InitialBackoff": "1s",
				  "MaxBackoff": "6s",
				  "BackoffMultiplier": 1.5,
				  "RetryableStatusCodes": [ "UNAVAILABLE" ]
			  }
			}]}`), grpc.WithBlock())
				if err != nil {
					c.Logging.Errorf("could not connect to statistic service:%v, waiting for 10sec to retry", err)
					c.IsStatsClientConnected = false
					break
				}
				c.Logging.Debugf("connected to the statistic server:%v with name:%v and id:%v", *statsServer, c.StatsClient.GetName(), c.StatsClient.GetId())
				c.StatsConnClient = proto.NewStatsClient(conn)
				c.IsStatsClientConnected = true
			}
			c.Logging.Debug("cheking statistic server connection in 1sec")
			time.Sleep(1 * time.Second)
		}
	}()

	for {
		select {
		case stat := <-c.Statschannel:
			c.Logging.Tracef("received client statistics:%v", stat)
			if c.IsStatsClientConnected {
				stream, err := c.StatsConnClient.RecordStats(context.Background())
				if err != nil {
					c.Logging.Errorf("statistic service registration failed:%v", err)
					c.IsStatsClientConnected = false
					break
				}
				if err := stream.Send(stat); err != nil {
					c.Logging.Errorf("can not send stats:%v, err:%v", stream, err)
					c.IsStatsClientConnected = false
					break
				}
				c.Logging.Tracef("sent client statistics:%v", stat)
				break
			}
			c.IsStatsClientConnected = false
			c.Logging.Tracef("statistic server is not redy, can not sent client statistics:%v", stat)
		}
	}
}

func main() {
	c.Logging.Infof("client is initialized with parameters:%v", c)
	go getMonitoringObjects(c)
	go connectStatsServer(c)
	c.Run()
	c.WaitGroup.Add(1)
	go func() {
		defer c.WaitGroup.Done()
	}()
	go func() {
		c.WaitGroup.Wait()
	}()
}

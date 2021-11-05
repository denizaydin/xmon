package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/denizaydin/xmon/proto"
	"github.com/fsnotify/fsnotify"
	logrus "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
)

//server - configuration server
var server *Server

//MonitoringObjects - Objects to be monitored, ping, trace, resolve which are defined in the proto file.
type MonitoringObjects struct {
	MonitorObjects *map[string]*proto.MonitoringObject
}

//ClientConnection - Client connection
type ClientConnection struct {
	stream         proto.ConfigServer_CreateStreamServer
	client         *proto.Client
	net            string
	active         bool
	lastactivetime int64
	error          chan error
}

//Server - Holds varibles of the server
type Server struct {
	//ServerAddr - net address
	ServerAddr string
	//Current monitoring objects
	MonitorObjects map[string]*proto.MonitoringObject
	//Current connected clients
	Connections map[string]*ClientConnection
	//Client update time. Current monitoring object will be sent to the clients at this period.
	//We do not any special message to client to remove deleted ones. Client supposed to watch this data and kill the monitoring if its not updated within maxruntime variable of monitoring object.
	ClientUpdateInterval int
	//Data file name
	DataFileName string
	//Data file path
	DataFilePath string
	//Logging
	Logging *logrus.Logger
}

func init() {
	//server - server instance.
	server = &Server{
		//MonitorObjects - Holds the monitoring objects.
		MonitorObjects: map[string]*proto.MonitoringObject{},
		//Connections- Holds connected clients.
		Connections: map[string]*ClientConnection{},
		//ClientUpdateInterval- How often configuration data will be send.
		ClientUpdateInterval: 5,
		//Logging - Current logging.
		Logging: &logrus.Logger{},
	}
	server.MonitorObjects = make(map[string]*proto.MonitoringObject)
	server.Logging = logrus.New()
	// Log as JSON instead of the default ASCII formatter.
	server.Logging.SetFormatter(&logrus.TextFormatter{
		DisableColors: false,
		FullTimestamp: true,
	})
	server.Logging.SetOutput(os.Stdout)
	logLevel := "info"
	flag.StringVar(&logLevel, "loglevel", "info", "disable,info, error, warning,debug or trace")
	flag.StringVar(&server.ServerAddr, "addr", "localhost:8080", "server net address")
	flag.IntVar(&server.ClientUpdateInterval, "updateinterval", 5, "full configuration sent interval time in seconds, use lower values for better results as we are using grpc streaming")
	flag.StringVar(&server.DataFileName, "datafilename", "dataConfig.json", "monitoring objects data file name as json")
	flag.StringVar(&server.DataFilePath, "datafilepath", ".", "monitoring objects data file path, default is current directory")
	flag.Parse()
	switch logLevel {
	case "disable":
		server.Logging.SetOutput(ioutil.Discard)
	case "info":
		server.Logging.SetLevel(logrus.InfoLevel)
	case "error":
		server.Logging.SetLevel(logrus.ErrorLevel)
	case "warn":
		server.Logging.SetLevel(logrus.WarnLevel)
	case "debug":
		server.Logging.SetLevel(logrus.DebugLevel)
	case "trace":
		server.Logging.SetLevel(logrus.TraceLevel)
	default:
		server.Logging.SetLevel(logrus.DebugLevel)
	}
}

// CreateStream - Creates a connection, appends the connection to the servers connetion slice, and returns a channel error
func (s *Server) CreateStream(pconn *proto.Connect, stream proto.ConfigServer_CreateStreamServer) error {
	pr, ok := peer.FromContext(stream.Context())
	if !ok {
		server.Logging.Warningf("configserver: cannot get client ip address!! %v", pr.Addr)
	}
	server.Logging.Debugf("configserver: received client connection request from ip %v", pr.Addr)
	//Parsing client's ip address
	clientIP, _, _ := net.SplitHostPort(pr.Addr.String())
	clientName := pconn.Client.GetName()
	if clientName == "" {
		server.Logging.Warnf("configserver: client name is empty, using client ip address:%v as its name", clientIP)
		return fmt.Errorf("configserver: client name cannot be empty")
	}
	server.Logging.Infof("configserver: registering or updating client:%v, groups:%v  as ping:%v, as trace:%v, as app:%v", pconn.Client.GetName(), pconn.Client.GetGroups(), pconn.Client.GetAddAsPingDest(), pconn.Client.GetAddAsTraceDest(), pconn.Client.GetAddAsAppDest())
	// may be reject client requests from the same ip address!
	s.Connections[clientName] = &ClientConnection{
		stream:         stream,
		client:         pconn.Client,
		net:            pr.Addr.String(),
		active:         true,
		lastactivetime: time.Now().UnixNano(),
		error:          make(chan error),
	}
	return <-s.Connections[pconn.Client.GetName()].error
}

//removeLongDeadClients - checks the current connections for clients which are inactive more than 4 update time interval.
func removeLongDeadClients(s *Server) {
	go func() {
		for {
			time.Sleep(4 * time.Duration(s.ClientUpdateInterval) * time.Second)
			s.Logging.Debugf("configserver: checking for connections which are not active more than 4 times update period:%v", s.ClientUpdateInterval)
			for key, conn := range s.Connections {
				if conn.active == false {
					server.Logging.Debugf("configserver: found inactive client:%v, last active time:%v and our time:%v", key, time.Unix(0, conn.lastactivetime), time.Now())
					if time.Now().UnixNano()-conn.lastactivetime > int64(time.Duration(4*s.ClientUpdateInterval*int(time.Second))) {
						delete(s.Connections, key)
						server.Logging.Infof("found inactive client:%v, removed from connection list", conn.client.Id)
					}
				}
			}
		}
	}()
}

func calculateUpdate(s *Server, updateClient *ClientConnection) map[string]*proto.MonitoringObject {
	var update = make(map[string]*proto.MonitoringObject)
	/* Calculation if clients wants to be monitored by other clients. This will add client information to the configuation update.
	Only ping methods is implemented
	*/
	for key, conn := range s.Connections {
		s.Logging.Tracef("configserver: checking if client:%v wants to be monitored, as ping:%v, as trace:%v, as app:%v by opther clients", key, conn.client.GetAddAsPingDest(), conn.client.GetAddAsTraceDest(), conn.client.GetAddAsAppDest())
		if conn.client.AddAsPingDest || conn.client.AddAsTraceDest || conn.client.AddAsAppDest {
			s.Logging.Tracef("configserver: client:%v wants to be monitored, as ping:%v, as trace:%v, as app:%v by opther clients", conn.client.GetAddAsPingDest(), conn.client.GetAddAsTraceDest(), conn.client.GetAddAsAppDest())
		}
		if !conn.active {
			s.Logging.Tracef("configserver: client:%v, conn name:%v is inactive,passing", conn.client.Name)
			continue
		}
		if updateClient.client.Name == conn.client.Name {
			s.Logging.Tracef("configserver: can not send client info to the same client")
			continue
		}
		//Checking if one of the client object groups will match one of clients groups which update will be send
		if func(g1 map[string]string, g2 map[string]string) bool {
			for group := range g1 {
				for updateClientGroup := range g2 {
					s.Logging.Tracef("configserver: checking client:%v group:%v, update client group:%v", key, group, updateClientGroup)
					if group == updateClientGroup {
						s.Logging.Tracef("configserver: checking client:%v group:%v with, matched update client group:%v", key, group, updateClientGroup)
						return true
					}
				}
			}
			return false
		}(conn.client.Groups, updateClient.client.Groups) {
			if conn.client.GetAddAsPingDest() {
				server.Logging.Tracef("configserver: client:%v, wants to the monitoried by ping", key, conn.client.Name)
				clientIP, _, _ := net.SplitHostPort(conn.net)
				update[conn.client.Id] = &proto.MonitoringObject{
					Object: &proto.MonitoringObject_Pingdest{
						Pingdest: &proto.PingDest{
							Destination: clientIP,
							Interval:    1000000000,
							PacketSize:  9000,
							Groups:      map[string]string{},
						},
					},
				}
				server.Logging.Debugf("configserver: checking client:%v, added ip address:%v client name:%v id:%v to the monitoring list as a ping destination", key, conn.client.Name, conn.client.Id)
			}
		}
	}
	for key, monObject := range s.MonitorObjects {
		switch monObject.Object.(type) {
		case *proto.MonitoringObject_Pingdest:
			//checking if one of the monitoring object groups will match one of the clients groups
			if func(g1 map[string]string, g2 map[string]string) bool {
				for group := range g1 {
					for conngroup := range g2 {
						s.Logging.Tracef("configserver: checking ping Object:%v group with update client group:%v", group, conngroup)
						if group == conngroup {
							s.Logging.Debugf("configserver: ping Object:%v matched with client group:%v", group, conngroup)
							return true
						}
					}
				}
				return false
			}(monObject.GetPingdest().Groups, updateClient.client.Groups) {
				s.Logging.Tracef("configserver: added ping Object:%v to the client update, object timeout to:%v", key, int32(3*s.ClientUpdateInterval))
				update[key] = &proto.MonitoringObject{
					Object: &proto.MonitoringObject_Pingdest{
						Pingdest: &proto.PingDest{
							Name:        monObject.GetPingdest().GetName(),
							Destination: monObject.GetPingdest().Destination,
							Interval:    monObject.GetPingdest().Interval,
							PacketSize:  monObject.GetPingdest().PacketSize,
							Ttl:         monObject.GetPingdest().Ttl,
						},
					},
				}
			}
		case *proto.MonitoringObject_Resolvedest:
			//checking if one of the monitoring object groups will match one of the clients groups
			if func(g1 map[string]string, g2 map[string]string) bool {
				for group := range g1 {
					for conngroup := range g2 {
						s.Logging.Tracef("configserver: checking resolve Object:%v group with update client group:%v", group, conngroup)
						if group == conngroup {
							s.Logging.Debugf("configserver: resolve Object:%v matched with client group:%v", group, conngroup)
							return true
						}
					}
				}
				return false
			}(monObject.GetResolvedest().Groups, updateClient.client.Groups) {
				s.Logging.Tracef("configserver: added resolve Object:%v to the client update, object timeout to:%v", key, int32(3*s.ClientUpdateInterval))
				update[key] = &proto.MonitoringObject{
					Object: &proto.MonitoringObject_Resolvedest{
						Resolvedest: &proto.ResolveDest{
							Name:        monObject.GetResolvedest().GetName(),
							Destination: monObject.GetResolvedest().Destination,
							Interval:    monObject.GetResolvedest().Interval,
							Resolver:    monObject.GetResolvedest().Resolver,
						},
					},
				}
			}
		case *proto.MonitoringObject_Tracedest:
			//checking if one of the monitoring object groups will match one of the clients groups
			if func(g1 map[string]string, g2 map[string]string) bool {
				for group := range g1 {
					for conngroup := range g2 {
						s.Logging.Tracef("configserver: checking trace Object:%v group with update client group:%v", group, conngroup)
						if group == conngroup {
							s.Logging.Debugf("configserver: trace Object:%v matched with client group:%v", group, conngroup)
							return true
						}
					}
				}
				return false
			}(monObject.GetTracedest().Groups, updateClient.client.Groups) {
				s.Logging.Tracef("configserver: checking resolve Object:%v to the client update, object timeout to:%v", key, int32(3*s.ClientUpdateInterval))
				update[key] = &proto.MonitoringObject{
					Object: &proto.MonitoringObject_Tracedest{
						Tracedest: &proto.TraceDest{
							Name:        monObject.GetTracedest().GetName(),
							Destination: monObject.GetTracedest().Destination,
							Interval:    monObject.GetTracedest().Interval,
							Ttl:         monObject.GetTracedest().Ttl,
						},
					},
				}
			}
		case *proto.MonitoringObject_Appdest:
			//checking if one of the monitoring object groups will match one of the clients groups
			if func(g1 map[string]string, g2 map[string]string) bool {
				for group := range g1 {
					for conngroup := range g2 {
						s.Logging.Tracef("configserver: checking app Object:%v group with update client group:%v", group, conngroup)
						if group == conngroup {
							s.Logging.Debugf("configserver: app Object:%v matched with client group:%v", group, conngroup)
							return true
						}
					}
				}
				return false
			}(monObject.GetAppdest().GetGroups(), updateClient.client.Groups) {
				s.Logging.Tracef("configserver: added ping Object:%v to the client update, object timeout to:%v", key, int32(3*s.ClientUpdateInterval))
				update[key] = &proto.MonitoringObject{
					Object: &proto.MonitoringObject_Appdest{
						Appdest: &proto.AppDest{
							Name:        monObject.GetAppdest().GetName(),
							Destination: monObject.GetAppdest().GetDestination(),
							Type:        monObject.GetAppdest().GetType(),
							Interval:    monObject.GetAppdest().GetInterval(),
						},
					},
				}
			}
		}

		s.Logging.Debugf("configserver: calculated update for the monitoring object:%v to the client:%v", update, updateClient.client.GetName())
	}
	return update
}

//broadcastData - Broadcasts configuration data to the connected clients
func broadcastData(s *Server) {
	ticker := time.NewTicker(time.Duration(s.ClientUpdateInterval) * time.Second)
	go func() {
		for {
			select {
			case <-ticker.C:
				s.Logging.Tracef("configserver: update time, number of connections for sending update:%v", len(s.Connections))
				//Check the registered clients
				for _, conn := range s.Connections {
					s.Logging.Debugf("configserver: sending update to the client:%v on net address:%v", conn.client.GetName(), conn.net)
					for key, monObject := range calculateUpdate(s, conn) { //convert seconds to millisecond
						monObject.UpdateInterval = int32(s.ClientUpdateInterval * 1000)
						s.Logging.Infof("configserver: sending update, monitoring object:%v to the client:%v", key, conn.client.GetName())
						go func(monObject *proto.MonitoringObject, conn *ClientConnection) {
							if conn.active {
								err := conn.stream.Send(monObject)
								if err != nil {
									s.Logging.Errorf("error:%v with stream:%s for client:%v address:%v", err, conn.stream, conn.client.GetName(), conn.net)
									conn.active = false
									conn.error <- err
								}
								s.Logging.Infof("configserver: sent update, monitoring object:%t to the client:%v", monObject.Object, conn.client.GetName())
							}
						}(monObject, conn)
					}
				}
			}
		}
	}()
}

/*getData - retrive configuration data periodically from a file
Appends monitoring object type to the names as same name can be used with multiple monitoring object type, like x.com for ping and resolve.
*/
func getData(s *Server) {
	monitoringObjects := make(map[string]*proto.MonitoringObject)
	pingdestinations := make(map[string]*proto.PingDest)
	tracedestinations := make(map[string]*proto.TraceDest)
	resolvedestinations := make(map[string]*proto.ResolveDest)
	appdestinations := make(map[string]*proto.AppDest)

	// Set the file name of the configurations file
	viper.SetConfigName(s.DataFileName)
	// Set the path to look for the configurations file
	viper.AddConfigPath(s.DataFilePath)
	viper.SetConfigType("json")
	viper.WatchConfig()
	viper.OnConfigChange(func(e fsnotify.Event) {
		server.Logging.Infof("config file changed:%v", e.Name)
		newmonitoringObjects := make(map[string]*proto.MonitoringObject)

		pingdestinations = make(map[string]*proto.PingDest)
		tracedestinations = make(map[string]*proto.TraceDest)
		resolvedestinations = make(map[string]*proto.ResolveDest)
		appdestinations = make(map[string]*proto.AppDest)

		viper.UnmarshalKey("pingdests", &pingdestinations)
		viper.UnmarshalKey("tracedests", &tracedestinations)
		viper.UnmarshalKey("resolvedests", &resolvedestinations)
		viper.UnmarshalKey("appdests", &appdestinations)

		for pingDest := range pingdestinations {
			pingdestinations[pingDest].Name = pingDest
			newmonitoringObjects[pingDest+"-ping"] = &proto.MonitoringObject{
				Object: &proto.MonitoringObject_Pingdest{
					Pingdest: pingdestinations[pingDest],
				},
			}
			newmonitoringObjects[pingDest+"-ping"].GetPingdest().Name = pingDest

		}
		for traceDest := range tracedestinations {
			tracedestinations[traceDest].Name = traceDest
			newmonitoringObjects[traceDest+"-trace"] = &proto.MonitoringObject{
				Object: &proto.MonitoringObject_Tracedest{
					Tracedest: tracedestinations[traceDest],
				},
			}
			newmonitoringObjects[traceDest+"-trace"].GetTracedest().Name = traceDest

		}
		for resolveDest := range resolvedestinations {
			resolvedestinations[resolveDest].Name = resolveDest
			newmonitoringObjects[resolveDest+"-resolve"] = &proto.MonitoringObject{
				Object: &proto.MonitoringObject_Resolvedest{
					Resolvedest: resolvedestinations[resolveDest],
				},
			}
			newmonitoringObjects[resolveDest+"-resolve"].GetResolvedest().Name = resolveDest

		}
		for appDest := range appdestinations {
			appdestinations[appDest].Name = appDest
			newmonitoringObjects[appDest+"-app"] = &proto.MonitoringObject{
				Object: &proto.MonitoringObject_Appdest{
					Appdest: appdestinations[appDest],
				},
			}
			newmonitoringObjects[appDest+"-app"].GetAppdest().Name = appDest
		}
		server.MonitorObjects = newmonitoringObjects
		server.Logging.Debugf("configserver: changed configuration data to %v", newmonitoringObjects)
	})
	server.Logging.Infof("configserver: using config: %s\n", viper.ConfigFileUsed())
	for {
		if err := viper.ReadInConfig(); err != nil {
			server.Logging.Errorf("configserver: can not read config file, retring in 10sec, %s", err)
			time.Sleep(10 * time.Second)
		} else {
			server.Logging.Debugf("configserver: read config file")
			break
		}
	}
	viper.UnmarshalKey("pingdests", &pingdestinations)
	viper.UnmarshalKey("tracedests", &tracedestinations)
	viper.UnmarshalKey("resolvedests", &resolvedestinations)
	viper.UnmarshalKey("appdests", &appdestinations)

	for pingDest := range pingdestinations {
		pingdestinations[pingDest].Name = pingDest
		monitoringObjects[pingDest+"-ping"] = &proto.MonitoringObject{
			Object: &proto.MonitoringObject_Pingdest{
				Pingdest: pingdestinations[pingDest],
			},
		}
		monitoringObjects[pingDest+"-ping"].GetPingdest().Name = pingDest
	}
	for traceDest := range tracedestinations {
		tracedestinations[traceDest].Name = traceDest
		monitoringObjects[traceDest+"-trace"] = &proto.MonitoringObject{
			Object: &proto.MonitoringObject_Tracedest{Tracedest: tracedestinations[traceDest]},
		}
		monitoringObjects[traceDest+"-trace"].GetTracedest().Name = traceDest
	}
	for resolveDest := range resolvedestinations {
		resolvedestinations[resolveDest].Name = resolveDest
		monitoringObjects[resolveDest+"-resolve"] = &proto.MonitoringObject{
			Object: &proto.MonitoringObject_Resolvedest{
				Resolvedest: resolvedestinations[resolveDest],
			},
		}
		monitoringObjects[resolveDest+"-resolve"].GetResolvedest().Name = resolveDest
	}
	for appDest := range appdestinations {
		appdestinations[appDest].Name = appDest
		monitoringObjects[appDest+"-app"] = &proto.MonitoringObject{
			Object: &proto.MonitoringObject_Appdest{
				Appdest: appdestinations[appDest],
			},
		}
		monitoringObjects[appDest+"-app"].GetAppdest().Name = appDest
	}
	server.Logging.Debugf("configserver: returning configuration data %v", monitoringObjects)
	server.MonitorObjects = monitoringObjects
}
func main() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs)
	server.Logging.Infof("configserver: server is initialized with parameters:%+v", server)
	go func() {
		for {
			s := <-sigs
			switch s {
			case syscall.SIGURG:
				server.Logging.Infof("received unhandled %v signal from os", s)
			default:
				server.Logging.Infof("received %v signal from os,exiting", s)
				os.Exit(1)
			}
		}
	}()
	go getData(server)
	grpcServer := grpc.NewServer(grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
		MinTime:             5 * time.Second, // If a client pings more than once every 5 seconds, terminate the connection
		PermitWithoutStream: true,            // Allow pings even when there are no active streams
	}), grpc.KeepaliveParams(keepalive.ServerParameters{
		MaxConnectionIdle: 15 * time.Second, // If a client is idle for 15 seconds, send a GOAWAY
		Time:              5 * time.Second,  // Ping the client if it is idle for 5 seconds to ensure the connection is still active
		Timeout:           1 * time.Second,  // Wait 1 second for the ping ack before assuming the connection is dead
	}))
	server.Logging.Debugf("configserver: got the configuration data:%v", server.MonitorObjects)
	server.Logging.Infof("configserver: starting server at:%v", server.ServerAddr)
	listener, err := net.Listen("tcp", server.ServerAddr)
	if err != nil {
		server.Logging.Fatalf("configserver: error creating the server %v", err)
	}
	go broadcastData(server)
	go removeLongDeadClients(server)
	proto.RegisterConfigServerServer(grpcServer, server)
	server.Logging.Infof("configserver: started server at:%v", listener.Addr())
	grpcServer.Serve(listener)
	select {}
}

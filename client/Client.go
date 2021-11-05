//Package client - includes all required monitoring objects for the client
package client

import (
	"sync"
	"time"

	proto "github.com/denizaydin/xmon/proto"
	"github.com/sirupsen/logrus"
)

//MonObject - Stores the monitoring object and its running thread properties;
type MonObject struct {
	//ConfigurationUpdatetime: Stores the timestamp of the last configuration update for the monitorobject from the server
	ConfigurationUpdatetime time.Time
	//ThreadupdateTime: Stores the timestamp of setted from the thread of the monitoring object. Used for detecting thread aliveness, ThreadupdateTime must not be bigger than thread interval.
	ThreadupdateTime time.Time
	//ThreadInterval: Stores the thread last interval. Used for detected dead threads. Monobject interval can be updated any time, threadInterval is used for race conditions.
	ThreadInterval int32
	//Object: Stores monitoring object
	Object *proto.MonitoringObject
	//Notify: Used for thread communications, stopping thread e.t.c
	Notify chan string
}

//XmonClient - Holds client parameters
type XmonClient struct {
	//ConfigClient: Stores configuration client params
	ConfigClient            *proto.Client
	IsConfigClientConnected bool
	//StatsClient: Stores statistic client params
	StatsClient            *proto.Client
	StatsConnClient        proto.StatsClient
	IsStatsClientConnected bool
	Statschannel           chan *proto.StatsObject
	MonObecjts             map[string]*MonObject
	MonObjectScanTimer     *time.Ticker
	Logging                *logrus.Logger
	WaitChannel            chan int
	WaitGroup              *sync.WaitGroup
}

//runningPingObjects - Total number of ping monitoring objects
var runningPingObjects map[string]*MonObject

//runningResolveObjects - Total number of dns resolve monitoring objects
var runningResolveObjects map[string]*MonObject

//runningTraceObjects - Total number of traceroute monitoring objects
var runningTraceObjects map[string]*MonObject

//runningAppObjects - Total number of traceroute monitoring objects
var runningAppObjects map[string]*MonObject

//Run - This is blocking function that will create, delete or update monitoring objects of the client.
func (client *XmonClient) Run() {
	client.Logging.Trace("Client: runninf monitoring objects checks which will create, update or delete them")
	runningPingObjects := make(map[string]*MonObject)
	runningResolveObjects := make(map[string]*MonObject)
	runningTraceObjects := make(map[string]*MonObject)
	runningAppObjects := make(map[string]*MonObject)

	for {
		client.Logging.Debugf("Client: current running number of monitor objects, ping:%v, resolve:%v, trace:%v, app:%v", len(runningPingObjects), len(runningResolveObjects), len(runningTraceObjects), len(runningAppObjects))
		for key, monObject := range client.MonObecjts {
			switch t := monObject.Object.Object.(type) {
			case *proto.MonitoringObject_Pingdest:
				client.Logging.Tracef("Client: checking ping object:%v", monObject)
				if _, ok := runningPingObjects[key]; ok {
					configurationUpdateDiff := time.Now().Sub(monObject.ConfigurationUpdatetime).Milliseconds()
					threadUpdateDiff := time.Now().Sub(runningPingObjects[key].ThreadupdateTime).Milliseconds()
					client.Logging.Tracef("Client: pinger:%v current time:%v, configuration updated:%v msec ago, threadupdated:%v msec ago, timeout:%v sec and interval:%v msec", key, time.Now().UnixNano(), configurationUpdateDiff, threadUpdateDiff, monObject.Object.UpdateInterval, monObject.Object.GetPingdest().GetInterval())
					if configurationUpdateDiff > int64(monObject.Object.UpdateInterval*2) {
						client.Logging.Warnf("Client: found timeout ping:%v object configuration updated:%v , stopping it", key, configurationUpdateDiff)
						runningPingObjects[key].Notify <- "done"
						client.Logging.Tracef("Client: stopped ping:%v object, removing from running ping object list", key)
						delete(runningPingObjects, key)
						client.Logging.Tracef("Client: removing ping:%v object from the ping object list", key)
						delete(client.MonObecjts, key)
						client.Logging.Debugf("Client: deleted ping:%v object", key)
					} else if time.Now().Sub(runningPingObjects[key].ThreadupdateTime).Milliseconds() > (int64(runningPingObjects[key].Object.GetPingdest().Interval) * 2) {
						client.Logging.Errorf("Client: found dead ping:%v object thread updated:%v msec ago, respawning", key, threadUpdateDiff)
						runningPingObjects[key] = &MonObject{
							ConfigurationUpdatetime: time.Now(),
							Object:                  monObject.Object,
							Notify:                  make(chan string),
						}
						go CheckIcmpPing(runningPingObjects[key], client)
					} else {
						client.Logging.Debugf("Client: updating ping object:%v, values:%v", key, monObject.Object.GetPingdest())
						runningPingObjects[key].Object.GetPingdest().Destination = monObject.Object.GetPingdest().GetDestination()
						runningPingObjects[key].Object.GetPingdest().Interval = monObject.Object.GetPingdest().GetInterval()
						runningPingObjects[key].Object.GetPingdest().PacketSize = monObject.Object.GetPingdest().GetPacketSize()
						runningPingObjects[key].Object.GetPingdest().Ttl = monObject.Object.GetPingdest().GetTtl()
					}
				} else {
					runningPingObjects[key] = &MonObject{
						ConfigurationUpdatetime: time.Now(),
						Object:                  monObject.Object,
						Notify:                  make(chan string),
					}
					client.Logging.Infof("Client: creating ping object:%v, values:%v", key, monObject.Object.GetPingdest())
					go CheckIcmpPing(runningPingObjects[key], client)
				}
			case *proto.MonitoringObject_Resolvedest:
				client.Logging.Tracef("Client: checking resolve object:%v", monObject)
				if _, ok := runningResolveObjects[key]; ok {
					configurationUpdateDiff := time.Now().Sub(monObject.ConfigurationUpdatetime).Milliseconds()
					threadUpdateDiff := time.Now().Sub(runningResolveObjects[key].ThreadupdateTime).Milliseconds()
					client.Logging.Tracef("Client: resolver:%v current time:%v, configuration updated:%v msec ago, threadupdated:%v msec ago, timeout:%v sec and interval:%v msec", key, time.Now().UnixNano(), configurationUpdateDiff, threadUpdateDiff, monObject.Object.UpdateInterval, monObject.Object.GetResolvedest().GetInterval())
					if configurationUpdateDiff > int64(monObject.Object.UpdateInterval*2) {
						client.Logging.Warnf("Client: found timeout resolve:%v object configuration updated:%v , stopping it", key, configurationUpdateDiff)
						runningResolveObjects[key].Notify <- "done"
						client.Logging.Tracef("Client: stopped resolve:%v object, removing from running resolve object list", key)
						delete(runningResolveObjects, key)
						client.Logging.Tracef("Client: removing resolve:%v object from the resolve object list", key)
						delete(client.MonObecjts, key)
						client.Logging.Debugf("Client: deleted resolve:%v object", key)
					} else if time.Now().Sub(runningResolveObjects[key].ThreadupdateTime).Milliseconds() > (int64(runningResolveObjects[key].Object.GetResolvedest().Interval) * 2) {
						client.Logging.Errorf("Client: found dead resolve:%v object thread updated:%v msec ago, respawning", key, threadUpdateDiff)
						runningResolveObjects[key] = &MonObject{
							ConfigurationUpdatetime: time.Now(),
							Object:                  monObject.Object,
							Notify:                  make(chan string),
						}
						go CheckResolveDestination(runningResolveObjects[key], client)
					} else {
						client.Logging.Debugf("Client: updating resolve object:%v, values:%v", key, monObject.Object.GetResolvedest())
						runningResolveObjects[key].Object.GetResolvedest().Destination = monObject.Object.GetResolvedest().GetDestination()
						runningResolveObjects[key].Object.GetResolvedest().Interval = monObject.Object.GetResolvedest().GetInterval()
						runningResolveObjects[key].Object.GetResolvedest().Resolver = monObject.Object.GetResolvedest().GetResolver()
					}
				} else {
					runningResolveObjects[key] = &MonObject{
						ConfigurationUpdatetime: time.Now(),
						Object:                  monObject.Object,
						Notify:                  make(chan string),
					}
					client.Logging.Infof("Client: creating resolve object:%v, values:%v", key, monObject.Object.GetResolvedest())
					go CheckResolveDestination(runningResolveObjects[key], client)
				}
			case *proto.MonitoringObject_Tracedest:
				client.Logging.Tracef("Client: checking resolve object:%v", monObject)
				if _, ok := runningTraceObjects[key]; ok {
					configurationUpdateDiff := time.Now().Sub(monObject.ConfigurationUpdatetime).Milliseconds()
					threadUpdateDiff := time.Now().Sub(runningTraceObjects[key].ThreadupdateTime).Milliseconds()
					client.Logging.Tracef("Client: trace:%v current time:%v, configuration updated:%v msec ago, threadupdated:%v msec ago, timeout:%v sec and interval:%v msec", key, time.Now().UnixNano(), configurationUpdateDiff, threadUpdateDiff, monObject.Object.UpdateInterval, monObject.Object.GetTracedest().GetInterval())
					if configurationUpdateDiff > int64(monObject.Object.UpdateInterval*2) {
						client.Logging.Warnf("Client: found timeout trace:%v object configuration updated:%v , stopping it", key, configurationUpdateDiff)
						runningTraceObjects[key].Notify <- "done"
						client.Logging.Tracef("Client: stopped trace:%v object, removing from running trace object list", key)
						delete(runningTraceObjects, key)
						client.Logging.Tracef("Client: removing trace:%v object from the trace object list", key)
						delete(client.MonObecjts, key)
						client.Logging.Debugf("Client: deleted trace:%v object", key)
					} else if time.Now().Sub(runningTraceObjects[key].ThreadupdateTime).Milliseconds() > (int64(runningTraceObjects[key].Object.GetTracedest().Interval) * 2) {
						client.Logging.Errorf("Client: found dead resolve:%v object thread updated:%v msec ago, respawning", key, threadUpdateDiff)
						runningTraceObjects[key] = &MonObject{
							ConfigurationUpdatetime: time.Now(),
							Object:                  monObject.Object,
							Notify:                  make(chan string),
						}
						go CheckTraceDestination(runningTraceObjects[key], client)
					} else {
						client.Logging.Debugf("Client: updating trace object:%v, values:%v", key, monObject.Object.GetTracedest())
						runningTraceObjects[key].Object.GetTracedest().Destination = monObject.Object.GetTracedest().GetDestination()
						runningTraceObjects[key].Object.GetTracedest().Interval = monObject.Object.GetTracedest().GetInterval()
						runningTraceObjects[key].Object.GetTracedest().Ttl = monObject.Object.GetTracedest().GetTtl()

					}
				} else {
					runningTraceObjects[key] = &MonObject{
						ConfigurationUpdatetime: time.Now(),
						Object:                  monObject.Object,
						Notify:                  make(chan string),
					}
					client.Logging.Infof("Client: creating trace object:%v, values:%v", key, monObject.Object.GetResolvedest())
					go CheckTraceDestination(runningTraceObjects[key], client)
				}
			case *proto.MonitoringObject_Appdest:
				client.Logging.Tracef("Client: checking app object:%v", monObject)
				if _, ok := runningAppObjects[key]; ok {
					configurationUpdateDiff := time.Now().Sub(monObject.ConfigurationUpdatetime).Milliseconds()
					threadUpdateDiff := time.Now().Sub(runningAppObjects[key].ThreadupdateTime).Milliseconds()
					client.Logging.Tracef("Client: apper:%v current time:%v, configuration updated:%v msec ago, threadupdated:%v msec ago, timeout:%v sec and interval:%v msec", key, time.Now().UnixNano(), configurationUpdateDiff, threadUpdateDiff, monObject.Object.UpdateInterval, monObject.Object.GetAppdest().GetInterval())
					if configurationUpdateDiff > int64(monObject.Object.UpdateInterval*2) {
						client.Logging.Warnf("Client: found timeout ping:%v object configuration updated:%v , stopping it", key, configurationUpdateDiff)
						runningAppObjects[key].Notify <- "done"
						client.Logging.Tracef("Client: stopped app:%v object, removing from running ping object list", key)
						delete(runningAppObjects, key)
						client.Logging.Trace("Client: removing app:%v object from the ping object list", key)
						delete(client.MonObecjts, key)
						client.Logging.Debugf("Client: deleted app:%v object", key)
					} else if time.Now().Sub(runningAppObjects[key].ThreadupdateTime).Milliseconds() > (int64(runningAppObjects[key].Object.GetAppdest().Interval) * 2) {
						if runningAppObjects[key].Object.GetAppdest().GetType() == 2 {
							client.Logging.Errorf("Client: found dead app:%v object thread updated:%v msec ago, respawning", key, threadUpdateDiff)
							runningAppObjects[key] = &MonObject{
								ConfigurationUpdatetime: time.Now(),
								Object:                  monObject.Object,
								Notify:                  make(chan string),
							}
							go CheckGRPCServer(runningAppObjects[key], client)
						} else {
							client.Logging.Warnf("Client: unimplemented app monitoring object type:%T", runningAppObjects[key].Object.GetAppdest().GetType())
						}
					} else {
						if runningAppObjects[key].Object.GetAppdest().GetType() == 2 {
							client.Logging.Debugf("Client: updating app object:%v, values:%v", key, monObject.Object.GetAppdest())
							runningAppObjects[key].Object.GetAppdest().Destination = monObject.Object.GetAppdest().GetDestination()
							runningAppObjects[key].Object.GetAppdest().Interval = monObject.Object.GetAppdest().GetInterval()
						} else {
							client.Logging.Warnf("Client: unimplemented app monitoring object type:%T", runningAppObjects[key].Object.GetAppdest().GetType())
						}

					}
				} else {
					if monObject.Object.GetAppdest().GetType() == 2 {
						runningAppObjects[key] = &MonObject{
							ConfigurationUpdatetime: time.Now(),
							Object:                  monObject.Object,
							Notify:                  make(chan string),
						}
						client.Logging.Infof("Client: creating app object:%v, values:", key, monObject.Object.GetAppdest())
						go CheckGRPCServer(runningAppObjects[key], client)
					} else {
						client.Logging.Warnf("Client: unimplemented app monitoring object type:%T", runningAppObjects[key].Object.GetAppdest().GetType())
					}
				}
			default:
				client.Logging.Warnf("Client: unimplemented monitoring object type:%T", t)
			}
		}
		client.Logging.Trace("Client: sleeping for 1sec for scanning monobjects")
		stat := &proto.StatsObject{
			Client:    client.StatsClient,
			Timestamp: time.Now().UnixNano(),
			Object: &proto.StatsObject_Clientstat{
				Clientstat: &proto.ClientStat{
					ConfiguredMonObjects: int32(len(client.MonObecjts)),
					RunningMonObjects:    int32(len(runningPingObjects) + len(runningTraceObjects) + len(runningResolveObjects) + len(runningAppObjects)),
				},
			},
		}
		select {
		case client.Statschannel <- stat:
			client.Logging.Tracef("Client: sent client stat:%v", stat)
		default:
			client.Logging.Errorf("Client: can not send client stat:%v", stat)
		}
		client.Logging.Tracef("Client: Sleeping for 1 sec")
		time.Sleep(1 * time.Second)
	}
}

//Stop - Stops all running monitor objects
func (client *XmonClient) Stop() {
	client.Logging.Debug("Client: stopping all monitoring objects")
	for key := range runningPingObjects {
		client.Logging.Tracef("Client: tring to stop ping:%v object", key)
		runningPingObjects[key].Notify <- "done"
		client.Logging.Tracef("Client: stopped ping:%v object", key)
	}
	for key := range runningResolveObjects {
		client.Logging.Tracef("Client: tring to stop resolve:%v object", key)
		runningResolveObjects[key].Notify <- "done"
		client.Logging.Tracef("Client: stopped resolve:%v object", key)
	}
	for key := range runningTraceObjects {
		client.Logging.Tracef("Client: tring to stop trace:%v object", key)
		runningTraceObjects[key].Notify <- "done"
		client.Logging.Tracef("Client: stopped trace:%v object", key)
	}
	client.Logging.Debug("Client: stopped all monitoring objects")
}

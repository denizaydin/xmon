package main

import (
	"context"
	"flag"
	"net"
	"time"

	proto "github.com/denizaydin/xmon/proto"
	"github.com/sirupsen/logrus"

	"google.golang.org/grpc"
)

var log *logrus.Logger
var address string
var responseDelay int64

// server implements EchoServer.
type server struct {
	server *proto.UnaryEchoServiceServer
}

func init() {
	log = logrus.New()
	log.SetLevel(logrus.TraceLevel)
	log.SetFormatter(&logrus.TextFormatter{
		DisableColors: false,
		FullTimestamp: true,
	})
	flag.StringVar(&address, "address", "localhost:8888", "address")
	flag.Int64Var(&responseDelay, "responseDelay", 200, "response delay for the requests in milliseconds")
	flag.Parse()
}
func (s *server) UnaryEcho(ctx context.Context, req *proto.XmonEchoRequest) (*proto.XmonEchoResponse, error) {
	log.Infof("received req:%v from:%v", req)
	receiveTimestamp := time.Now().UnixNano()
	time.Sleep(time.Duration(time.Duration(responseDelay) * time.Millisecond))
	return &proto.XmonEchoResponse{
		ClientSendTimestamp:    req.ClientSendTimestamp,
		ServerReceiveTimestamp: receiveTimestamp,
		ServerSendTimestamp:    time.Now().UnixNano(),
		ServerDelay:            responseDelay * 1000000,
	}, nil
}
func main() {
	flag.Parse()
	lis, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	// Log as JSON instead of the default ASCII formatter.
	log.Infof("serving on address:%v with response delay:%v", address, responseDelay)
	s := grpc.NewServer()
	proto.RegisterUnaryEchoServiceServer(s, &server{})
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

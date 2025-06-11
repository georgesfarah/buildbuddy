package rpcz_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/buildbuddy-io/buildbuddy/server/testutil/testport"
	"github.com/buildbuddy-io/buildbuddy/server/util/rpcz"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	bspb "google.golang.org/genproto/googleapis/bytestream"
)

type testService struct{}

const streamingMessages = 3

func (s *testService) Read(_ *bspb.ReadRequest, stream bspb.ByteStream_ReadServer) error {
	for range streamingMessages {
		if err := stream.Send(nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *testService) Write(stream bspb.ByteStream_WriteServer) error {
	for {
		if _, err := stream.Recv(); err != nil {
			break
		}
	}
	return stream.SendAndClose(nil)
}

func (s *testService) QueryWriteStatus(context.Context, *bspb.QueryWriteStatusRequest) (*bspb.QueryWriteStatusResponse, error) {
	return nil, nil
}

func BenchmarkStatsHandlerOverhead(b *testing.B) {
	ctx := context.Background()

	for _, rpcMix := range []string{"unary", "server_streaming", "client_streaming", "all"} {
		for _, clientOption := range []bool{false, true} {
			for _, serverOption := range []bool{false, true} {
				b.Run(fmt.Sprintf("rpcMix=%v/clientRpcz=%v/serverRpcz=%v", rpcMix, clientOption, serverOption), func(b *testing.B) {
					handler := rpcz.NewHandler()

					// Setup server
					addr := fmt.Sprintf("localhost:%d", testport.FindFree(b))
					lis, err := net.Listen("tcp", addr)
					require.NoError(b, err)
					var serverOpts []grpc.ServerOption
					if serverOption {
						serverOpts = append(serverOpts, grpc.StatsHandler(handler.ServerStatsHandler()))
					}
					grpcServer := grpc.NewServer(serverOpts...)
					bspb.RegisterByteStreamServer(grpcServer, &testService{})

					go func() {
						require.NoError(b, grpcServer.Serve(lis))
					}()
					waitForServerReady(addr, 10*time.Second)
					defer grpcServer.Stop()

					// Setup client
					clientOpts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
					if clientOption {
						clientOpts = append(clientOpts, grpc.WithStatsHandler(handler.ClientStatsHandler()))
					}
					conn, err := grpc.NewClient(lis.Addr().String(), clientOpts...)
					require.NoError(b, err)
					defer conn.Close()
					client := bspb.NewByteStreamClient(conn)

					var calls []func(context.Context, testing.TB, bspb.ByteStreamClient)
					if rpcMix == "unary" || rpcMix == "all" {
						calls = append(calls, unaryRPC)
					}
					if rpcMix == "server_streaming" || rpcMix == "all" {
						calls = append(calls, serverStreamingRPC)
					}
					if rpcMix == "client_streaming" || rpcMix == "all" {
						calls = append(calls, clientStreamingRPC)
					}
					b.ResetTimer()
					b.RunParallel(func(pb *testing.PB) {
						for pb.Next() {
							for _, call := range calls {
								call(ctx, b, client)

							}
						}
					})
				})
			}
		}
	}
}

func unaryRPC(ctx context.Context, t testing.TB, client bspb.ByteStreamClient) {
	_, err := client.QueryWriteStatus(ctx, nil)
	require.NoError(t, err)
}

func serverStreamingRPC(ctx context.Context, t testing.TB, client bspb.ByteStreamClient) {
	readClient, err := client.Read(ctx, nil)
	require.NoError(t, err)
	for {
		_, err := readClient.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
	}
}

func clientStreamingRPC(ctx context.Context, t testing.TB, client bspb.ByteStreamClient) {
	writeClient, err := client.Write(ctx)
	require.NoError(t, err)
	for i := 0; i < streamingMessages; i++ {
		require.NoError(t, writeClient.Send(nil))
	}
	_, err = writeClient.CloseAndRecv()
	require.NoError(t, err)
}

func waitForServerReady(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for server at %s", addr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

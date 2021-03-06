package chat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/autom8ter/machine"
	chatpb "github.com/autom8ter/machine/examples/gen/go/example/chat"
	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"
	"strings"
	"time"
)

type chat struct {
	logger  *zap.Logger
	machine *machine.Machine
}

func NewChatServer(logger *zap.Logger, machine *machine.Machine) chatpb.ChatServiceServer {
	return &chat{logger: logger, machine: machine}
}

type message struct {
	text  string
	email string
}

func (c *chat) Chat(server chatpb.ChatService_ChatServer) error {
	ctx, cancel := context.WithCancel(server.Context())
	defer cancel()
	email := emailFromContext(ctx)
	channel := channelFromContext(ctx)
	c.machine.Go(func(routine machine.Routine) {
		for {
			select {
			case <-routine.Context().Done():
				return
			case <-ctx.Done():
				return
			default:
				incoming, err := server.Recv()
				if err != nil {
					c.logger.Error("failed to receive incoming stream message",
						zap.String("channel", channel),
						zap.Error(err),
					)
					continue
				}
				if incoming.Text != "" {
					if err := routine.Publish(channel, &message{
						text:  incoming.Text,
						email: email,
					}); err != nil {
						c.logger.Error("failed to publish incoming stream message",
							zap.String("channel", channel),
							zap.Error(err),
						)
						continue
					}
				}
			}
		}
	})
	c.machine.Go(func(routine machine.Routine) {
		if err := routine.Subscribe(channel, func(obj interface{}) {
			if obj != nil {
				msg := obj.(*message)
				if err := server.Send(&chatpb.ChatResponse{
					Channel:   channel,
					Text:      msg.text,
					User:      msg.email,
					Timestamp: time.Now().String(),
				}); err != nil {
					c.logger.Error("failed to start subscription",
						zap.String("channel", channel),
						zap.Error(err),
					)
				}
			}
		}); err != nil {
			c.logger.Error("failed to setup subscription",
				zap.String("channel", channel),
				zap.Error(err),
			)
		}
	})
	select {
	case <-ctx.Done():
		break
	}
	return nil
}

func channelFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		arr := md.Get("X-CHANNEL")
		if len(arr) > 0 {
			return arr[0]
		}
	}
	return ""
}

func emailFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		arr := md.Get("Authorization")
		if len(arr) > 0 {
			bearerString := arr[0]
			bearerSplit := strings.Split(bearerString, "Bearer ")
			if len(bearerSplit) > 0 {
				jwt := strings.TrimSpace(bearerSplit[1])
				jwtSplit := strings.Split(jwt, ".")
				if len(jwtSplit) == 3 {
					bits, _ := base64.StdEncoding.DecodeString(jwtSplit[1])
					values := map[string]interface{}{}
					json.Unmarshal(bits, &values)
					if values["email"] != nil {
						return values["email"].(string)
					}
				}
			}
		}
	}
	return ""
}

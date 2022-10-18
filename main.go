package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

var Debug bool

func init() {
	Debug, _ = strconv.ParseBool(os.Getenv("DEBUG"))
}

var slackAPI *slack.Client

func main() {
	var port int
	flag.IntVar(&port, "port", 8888, "listen port number")
	flag.Parse()

	slackAPI = slack.New(
		os.Getenv("SLACK_BOT_TOKEN"),
		slack.OptionAppLevelToken(os.Getenv("SLACK_APP_TOKEN")),
		slack.OptionDebug(Debug),
		slack.OptionLog(log.New(os.Stdout, "api: ", log.Lshortfile|log.LstdFlags)),
	)
	authTest, authTestErr := slackAPI.AuthTest()
	if authTestErr != nil {
		fmt.Fprintf(os.Stderr, "SLACK_BOT_TOKEN is invalid: %v\n", authTestErr)
		os.Exit(1)
	}
	log.Println("selfUserID", authTest.UserID)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	wg.Add(2)
	go runWebSocketServer(ctx, &wg, port)
	go runSocketModeReceiver(ctx, &wg)
	wg.Wait()
}

func runSocketModeReceiver(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	sm := socketmode.New(
		slackAPI,
		socketmode.OptionDebug(Debug),
		socketmode.OptionLog(log.New(os.Stdout, "sm: ", log.Lshortfile|log.LstdFlags)),
	)
	go func() {
		for envelope := range sm.Events {
			log.Printf("Event received type: %s, event: %#v", envelope.Type, envelope.Data)
			switch envelope.Type {
			case socketmode.EventTypeEventsAPI:
				// イベント API のハンドリング
				// 3 秒以内にとりあえず ack
				sm.Ack(*envelope.Request)
				eventPayload, _ := envelope.Data.(slackevents.EventsAPIEvent)
				switch eventPayload.Type {
				case slackevents.CallbackEvent:
					switch event := eventPayload.InnerEvent.Data.(type) {
					case *slackevents.MessageEvent:
						pubsub.Publish(event)
					default:
						sm.Debugf("Skipped: %v", event)
					}
				default:
					sm.Debugf("unsupported Events API eventPayload received")
				}
			default:
				sm.Debugf("Skipped: %v", envelope.Type)
			}
		}
	}()
	sm.RunContext(ctx)
}

var upgrader = websocket.Upgrader{} // use default options
var pubsub = &PubSub{
	Subscribers: make(map[string]chan message, 100),
}

type PubSub struct {
	Subscribers map[string]chan message
	mu          sync.Mutex
}

func (p *PubSub) Subscribe() (chan message, func()) {
	id := uuid.New().String()
	ch := make(chan message)
	log.Println("[info] new subscriber", id)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Subscribers[id] = ch
	return ch, func() {
		p.Unsubscribe(id)
	}
}

func (p *PubSub) Unsubscribe(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	log.Println("[info] unsubscribe", id)
	delete(p.Subscribers, id)
}

func (p *PubSub) Publish(msg message) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, ch := range p.Subscribers {
		select {
		case ch <- msg:
		default:
			log.Printf("[warn] channel for %s is full", id)
		}
	}
}

func (p *PubSub) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, ch := range p.Subscribers {
		close(ch)
	}
	p.Subscribers = map[string]chan message{}
}

type message interface{}

func runWebSocketServer(ctx context.Context, wg *sync.WaitGroup, port int) {
	defer wg.Done()

	mux := http.NewServeMux()
	mux.HandleFunc("/websocket/", wsFunc)
	mux.HandleFunc("/start/", startFunc)
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil {
			if ctx.Err() != context.Canceled {
				log.Println("[error]", err)
			}
		}
	}()
	<-ctx.Done()
	log.Println("[info] shutdown websocket server")
	pubsub.Close()
	srv.Shutdown(ctx)
}

type StartResponse struct {
	OK    bool         `json:"ok"`
	URL   string       `json:"url"`
	Users []slack.User `json:"users"`
}

func startFunc(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 3 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	channelID := parts[2]
	log.Println("[info] new connection for", channelID)
	var users []slack.User
	if channelID != "" {
		userIDs, _, err := slackAPI.GetUsersInConversation(&slack.GetUsersInConversationParameters{
			ChannelID: channelID,
		})
		if err != nil {
			log.Println("[error] failed to get users in channel", channelID, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		log.Println("[info] channel members", userIDs)
		if len(userIDs) > 0 {
			us, err := slackAPI.GetUsersInfo(userIDs...)
			if err != nil {
				log.Println("[error] failed to get users info", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			users = *us
		}
	}

	r.Header.Set("Content-Type", "application/json")
	wsURL := fmt.Sprintf("ws://%s%s/websocket/", r.Host, r.URL.Port())
	log.Println("[info] websocket url", wsURL)
	json.NewEncoder(w).Encode(StartResponse{
		OK:    true,
		URL:   wsURL,
		Users: users,
	})
}

func wsFunc(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("[error]", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer conn.Close()
	defer time.Sleep(time.Second) // slow down

	if err := conn.WriteJSON(slack.Event{
		Type: "hello",
	}); err != nil {
		log.Println("[error]", err)
		return
	}

	ch, unsubscribe := pubsub.Subscribe()
	defer unsubscribe()

	go func() {
		for msg := range ch {
			if err := conn.WriteJSON(msg); err != nil {
				log.Println("[warn]", err)
			}
		}
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Println("[warn]", err)
			return
		}
		var event slack.Event
		if err := json.Unmarshal(msg, &event); err != nil {
			log.Printf("[warn] cannot decode message %s %s", string(msg), err)
			continue
		}
		if event.Type == "ping" {
			log.Println("[debug] ping received")
			if err := conn.WriteJSON(slack.Event{
				Type: "pong",
			}); err != nil {
				log.Println("[warn]", err)
			}
		} else {
			log.Printf("[info] recv: %s", msg)
		}
	}
}

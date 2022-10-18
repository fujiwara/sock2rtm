package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"

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

func main() {
	go runWebSocketServer(context.TODO())

	webApi := slack.New(
		os.Getenv("SLACK_BOT_TOKEN"),
		slack.OptionAppLevelToken(os.Getenv("SLACK_APP_TOKEN")),
		slack.OptionDebug(Debug),
		slack.OptionLog(log.New(os.Stdout, "api: ", log.Lshortfile|log.LstdFlags)),
	)
	socketMode := socketmode.New(
		webApi,
		socketmode.OptionDebug(Debug),
		socketmode.OptionLog(log.New(os.Stdout, "sm: ", log.Lshortfile|log.LstdFlags)),
	)
	authTest, authTestErr := webApi.AuthTest()
	if authTestErr != nil {
		fmt.Fprintf(os.Stderr, "SLACK_BOT_TOKEN is invalid: %v\n", authTestErr)
		os.Exit(1)
	}
	log.Println("selfUserID", authTest.UserID)

	go func() {
		for envelope := range socketMode.Events {
			log.Printf("Event received type: %s, event: %#v", envelope.Type, envelope.Data)
			switch envelope.Type {
			case socketmode.EventTypeEventsAPI:
				// イベント API のハンドリング
				// 3 秒以内にとりあえず ack
				socketMode.Ack(*envelope.Request)
				eventPayload, _ := envelope.Data.(slackevents.EventsAPIEvent)
				switch eventPayload.Type {
				case slackevents.CallbackEvent:
					switch event := eventPayload.InnerEvent.Data.(type) {
					case *slackevents.MessageEvent:
						pubsub.Publish(event)
					default:
						socketMode.Debugf("Skipped: %v", event)
					}
				default:
					socketMode.Debugf("unsupported Events API eventPayload received")
				}
			default:
				socketMode.Debugf("Skipped: %v", envelope.Type)
			}
		}
	}()
	socketMode.Run()
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

func runWebSocketServer(ctx context.Context) {
	http.HandleFunc("/websocket", wsFunc)
	http.HandleFunc("/connect", connectFunc)
	log.Fatal(http.ListenAndServe(":8888", nil))
}

func connectFunc(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		OK  bool   `json:"ok"`
		URL string `json:"url"`
	}{
		OK:  true,
		URL: "ws://localhost:8888/websocket",
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

	ch, unsubscribe := pubsub.Subscribe()
	defer unsubscribe()

	if err := conn.WriteJSON(slack.Event{
		Type: "hello",
	}); err != nil {
		log.Println("[error]", err)
		return
	}

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
			log.Println("[info] ping received")
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

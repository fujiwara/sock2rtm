package sock2rtm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/samber/lo"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

var Debug bool

type App struct {
	slackAPI *slack.Client
	port     int
	wg       sync.WaitGroup
	pubsub   *PubSub
	upgrader websocket.Upgrader
	metrics  Metrics
}

func New(port int) (*App, error) {
	app := &App{
		pubsub: NewPubSub(),
		port:   port,
		slackAPI: slack.New(
			os.Getenv("SLACK_BOT_TOKEN"),
			slack.OptionAppLevelToken(os.Getenv("SLACK_APP_TOKEN")),
			slack.OptionDebug(Debug),
		),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	authTest, authTestErr := app.slackAPI.AuthTestContext(ctx)
	if authTestErr != nil {
		return nil, fmt.Errorf("SLACK_BOT_TOKEN is invalid: %w", authTestErr)
	}
	log.Println("[info] selfUserID", authTest.UserID)
	return app, nil
}

func (app *App) Run(ctx context.Context) {
	app.wg.Add(2)
	go app.runWebSocketServer(ctx)
	go app.runSocketModeReceiver(ctx)
	app.wg.Wait()
}

func marshalJSONString(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (app *App) runSocketModeReceiver(ctx context.Context) {
	defer app.wg.Done()

	sm := socketmode.New(
		app.slackAPI,
		socketmode.OptionDebug(Debug),
	)
	go func() {
		for envelope := range sm.Events {
			data := marshalJSONString(envelope.Data)
			log.Printf("[debug] event received type: %s, event: %s", envelope.Type, data)
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
						atomic.AddInt64(&app.metrics.Messages.Received, 1)
						log.Printf("[debug] publish message event: %s", data)
						app.pubsub.Publish(event)
					default:
						atomic.AddInt64(&app.metrics.Messages.Unsupported, 1)
						log.Printf("[warn] skipped %s: %s", event, data)
					}
				default:
					atomic.AddInt64(&app.metrics.Messages.Unsupported, 1)
					log.Println("[warn] unsupported Events API eventPayload received. type:", eventPayload.Type)
				}
			case socketmode.EventTypeHello:
				atomic.AddInt64(&app.metrics.Slack.Hello, 1)
				log.Println("[info] socketmode hello")
			case socketmode.EventTypeDisconnect:
				atomic.AddInt64(&app.metrics.Slack.Disconnect, 1)
				log.Println("[info] socketmode disconnect event received")
			case socketmode.EventTypeConnecting:
				atomic.AddInt64(&app.metrics.Slack.Connecting, 1)
				log.Println("[info] socketmode connecting event received")
			case socketmode.EventTypeConnected:
				atomic.AddInt64(&app.metrics.Slack.Connected, 1)
				log.Println("[info] socketmode connect event received")
			default:
				atomic.AddInt64(&app.metrics.Messages.Unsupported, 1)
				log.Printf("[warn] skipped: %v", envelope.Type)
			}
		}
	}()
	sm.RunContext(ctx)
}

func (app *App) runWebSocketServer(ctx context.Context) {
	defer app.wg.Done()

	mux := http.NewServeMux()
	mux.HandleFunc("/websocket/", app.wsFunc)
	mux.HandleFunc("/start/", app.startFunc)
	mux.HandleFunc("/metrics", app.metricsFunc)
	addr := fmt.Sprintf(":%d", app.port)
	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	go func() {
		log.Println("[info] start websocket server", addr)
		if err := srv.ListenAndServe(); err != nil {
			if ctx.Err() != context.Canceled {
				log.Println("[error]", err)
			}
		}
	}()
	<-ctx.Done()
	log.Println("[info] shutdown websocket server")
	app.pubsub.Close()
	srv.Shutdown(ctx)
}

type StartResponse struct {
	OK    bool         `json:"ok"`
	URL   string       `json:"url"`
	Users []slack.User `json:"users"`
}

func parsePathParams(path string) ([]string, string, error) {
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		return nil, "", fmt.Errorf("invalid path: %s", path)
	}
	channelIDs := strings.Split(parts[2], ",")
	var clientID string
	if len(parts) >= 4 {
		clientID = parts[3]
	}
	return channelIDs, clientID, nil
}

func (app *App) startFunc(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	channelIDs, clientID, err := parsePathParams(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Println("[info] start for channels", channelIDs)
	usersMap := map[string]slack.User{}
	userIDs := []string{}
	for _, channelID := range channelIDs {
		uids, _, err := app.slackAPI.GetUsersInConversationContext(ctx, &slack.GetUsersInConversationParameters{
			ChannelID: channelID,
		})
		if err != nil {
			log.Println("[error] failed to get users in channel", channelID, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		log.Printf("[info] channel %s members %d %s", channelID, len(uids), uids)
		userIDs = append(userIDs, uids...)
	}
	userIDs = lo.Uniq(userIDs)

	if len(userIDs) > 0 {
		chunk := lo.Chunk(userIDs, 30) // 31以上だと too_many_users エラーになる
		for _, ids := range chunk {
			us, err := app.slackAPI.GetUsersInfoContext(ctx, ids...)
			if err != nil {
				log.Println("[error] failed to get users info", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			for _, u := range *us {
				u := u
				usersMap[u.ID] = u
			}
		}
	}
	users := make([]slack.User, 0, len(usersMap))
	for _, u := range usersMap {
		users = append(users, u)
	}

	wsURL := fmt.Sprintf("ws://%s%s/websocket/%s", r.Host, r.URL.Port(), strings.Join(channelIDs, ","))
	if clientID != "" {
		wsURL += "/" + clientID
	}
	log.Println("[info] websocket url", wsURL)
	json.NewEncoder(w).Encode(StartResponse{
		OK:    true,
		URL:   wsURL,
		Users: users,
	})
}

func (app *App) wsFunc(w http.ResponseWriter, r *http.Request) {
	channelIDs, clientID, err := parsePathParams(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	followChannels := sync.Map{}
	for _, id := range channelIDs {
		followChannels.Store(id, struct{}{})
	}
	filter := func(m Message) bool {
		switch m := m.(type) {
		case *slackevents.MessageEvent:
			_, ok := followChannels.Load(m.Channel)
			return ok
		default:
			return false
		}
	}

	conn, err := app.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("[error] failed to upgrade", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	atomic.AddInt64(&app.metrics.WebSocket.TotalConnections, 1)
	atomic.AddInt64(&app.metrics.WebSocket.CurrentConnections, 1)
	defer func() {
		time.Sleep(3 * time.Second) // slow down
		conn.Close()
		atomic.AddInt64(&app.metrics.WebSocket.CurrentConnections, -1)
	}()

	if err := conn.WriteJSON(slack.Event{
		Type: "hello",
	}); err != nil {
		log.Println("[error] failed to write hello", err)
		return
	}

	sub := app.pubsub.Subscribe(clientID, filter)
	defer sub.Unsubscribe()
	log.Println("[info] new websocket connection from", r.RemoteAddr, "for channels", channelIDs, "id", sub.ID)

	go func() {
		for msg := range sub.C {
			if err := conn.WriteJSON(msg); err != nil {
				atomic.AddInt64(&app.metrics.Messages.WriteErrored, 1)
				log.Printf("[warn] failed to write to %s %s", sub.ID, err)
			} else {
				atomic.AddInt64(&app.metrics.Messages.Delivered, 1)
			}
		}
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Printf("[warn] error from %s %s", sub.ID, err)
			return
		}
		var event slack.Event
		if err := json.Unmarshal(msg, &event); err != nil {
			log.Printf("[warn] cannot decode message from %s %s %s", sub.ID, string(msg), err)
			continue
		}
		if event.Type == "ping" {
			log.Println("[debug] ping received from", sub.ID)
			if err := conn.WriteJSON(slack.Event{
				Type: "pong",
			}); err != nil {
				log.Printf("[warn] faild to write json to %s %s", sub.ID, err)
			}
		} else {
			log.Printf("[info] recv: %s from %s", msg, sub.ID)
		}
	}
}

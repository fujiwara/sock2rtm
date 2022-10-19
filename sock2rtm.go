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
}

func New(port int) (*App, error) {
	app := &App{
		pubsub: &PubSub{
			Subscribers: make(map[string]*Subscriber),
		},
		port: port,
	}

	app.slackAPI = slack.New(
		os.Getenv("SLACK_BOT_TOKEN"),
		slack.OptionAppLevelToken(os.Getenv("SLACK_APP_TOKEN")),
		slack.OptionDebug(Debug),
		slack.OptionLog(log.New(os.Stdout, "api: ", log.Lshortfile|log.LstdFlags)),
	)
	authTest, authTestErr := app.slackAPI.AuthTest()
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
						log.Printf("[debug] publish message event: %s", data)
						app.pubsub.Publish(event)
					default:
						log.Printf("[debug] skipped %s: %s", event, data)
					}
				default:
					log.Println("[info] unsupported Events API eventPayload received. type:", eventPayload.Type)
				}
			default:
				log.Printf("[debug] skipped: %v", envelope.Type)
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

func parseChannelIDs(path string) ([]string, error) {
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid path: %s", path)
	}
	return strings.Split(parts[2], ","), nil
}

func (app *App) startFunc(w http.ResponseWriter, r *http.Request) {
	channelIDs, err := parseChannelIDs(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Println("[info] start for channels", channelIDs)
	usersMap := map[string]slack.User{}
	userIDs := []string{}
	for _, channelID := range channelIDs {
		uids, _, err := app.slackAPI.GetUsersInConversation(&slack.GetUsersInConversationParameters{
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
		chunk := lo.Chunk(userIDs, 30)
		for _, ids := range chunk {
			us, err := app.slackAPI.GetUsersInfo(ids...)
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

	r.Header.Set("Content-Type", "application/json")
	wsURL := fmt.Sprintf("ws://%s%s/websocket/%s", r.Host, r.URL.Port(), strings.Join(channelIDs, ","))
	log.Println("[info] websocket url", wsURL)
	json.NewEncoder(w).Encode(StartResponse{
		OK:    true,
		URL:   wsURL,
		Users: users,
	})
}

func (app *App) wsFunc(w http.ResponseWriter, r *http.Request) {
	channelIDs, err := parseChannelIDs(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	followChannels := make(map[string]bool, len(channelIDs))
	for _, id := range channelIDs {
		followChannels[id] = true
	}
	filter := func(m message) bool {
		switch m := m.(type) {
		case *slackevents.MessageEvent:
			_, ok := followChannels[m.Channel]
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
	defer conn.Close()
	defer time.Sleep(3 * time.Second) // slow down

	if err := conn.WriteJSON(slack.Event{
		Type: "hello",
	}); err != nil {
		log.Println("[error] failed to write hello", err)
		return
	}

	sub := app.pubsub.Subscribe(filter)
	defer sub.Unsubscribe()
	log.Println("[info] new websocket connection from", r.RemoteAddr, "for channels", channelIDs, "id", sub.ID)

	go func() {
		for msg := range sub.C {
			if err := conn.WriteJSON(msg); err != nil {
				log.Printf("[warn] failed to write to %s %s", sub.ID, err)
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

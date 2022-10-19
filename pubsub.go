package sock2rtm

import (
	"log"
	"sync"

	"github.com/google/uuid"
)

type PubSub struct {
	Subscribers map[string]*Subscriber
	mu          sync.Mutex
}

type Subscriber struct {
	ID          string
	C           chan Message
	Unsubscribe func()
	filter      func(Message) bool
}

type Message interface{}

func NewPubSub() *PubSub {
	return &PubSub{
		Subscribers: map[string]*Subscriber{},
	}
}

func (p *PubSub) Subscribe(filter func(Message) bool) *Subscriber {
	id := uuid.New().String()
	ch := make(chan Message)
	log.Println("[info] new subscriber", id)
	p.mu.Lock()
	defer p.mu.Unlock()
	s := &Subscriber{
		ID:          id,
		C:           ch,
		Unsubscribe: func() { p.Unsubscribe(id) },
		filter:      filter,
	}
	p.Subscribers[id] = s
	return s
}

func (p *PubSub) Unsubscribe(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	log.Println("[info] unsubscribe", id)
	delete(p.Subscribers, id)
}

func (p *PubSub) Publish(msg Message) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, s := range p.Subscribers {
		if s.filter != nil && !s.filter(msg) {
			log.Println("[debug] skip publish to", id)
			continue
		}
		select {
		case s.C <- msg:
		default:
			log.Printf("[warn] channel for %s is full", id)
		}
	}
}

func (p *PubSub) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.Subscribers {
		close(s.C)
	}
	p.Subscribers = map[string]*Subscriber{}
}

package natsclient

import (
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
)

// Connect dials url with self-healing options: if NATS is unreachable on the
// very first attempt, RetryOnFailedConnect makes nats.Connect return
// successfully anyway (with the *nats.Conn in a reconnecting state) instead
// of failing outright, and MaxReconnects(-1) means neither that initial
// outage nor a later one ever causes nats.go to give up retrying. This is
// what lets the dispatch side in main.go come alive on its own once NATS
// becomes reachable, per the design spec's "only the dispatch side is
// degraded until reconnect" — no restart required.
func Connect(url string) (*nats.Conn, error) {
	nc, err := nats.Connect(url,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			log.Printf("natsclient: disconnected: %v", err)
		}),
		nats.ReconnectHandler(func(c *nats.Conn) {
			log.Printf("natsclient: reconnected to %s", c.ConnectedUrl())
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("natsclient: connect to %s: %w", url, err)
	}
	return nc, nil
}

package pgutils

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/lib/pq"
)

// This listener exists because we cannot rely on lib/pq's built-in reconnect
// logic when using RDS IAM authentication. IAM auth uses short-lived tokens in
// the connection string; lib/pq will happily reconnect forever with the *same*
// DSN, but once the token expires those reconnect attempts can never succeed.
//
// To handle this, we treat pq.Listener as a disposable object and rebuild it
// whenever the connection is lost:
//
//   - PostgresqlConnector.GetConnectionString(ctx) returns a fresh DSN, which
//     includes a new IAM token.
//   - makeListener() uses that DSN to construct a new pq.Listener and issues
//     LISTEN pgChannelName.
//
// A goroutine runs the event loop: it drains listener.Notify,
// invokes the caller's callback for each notification, periodically calls
// Ping() to surface dead sockets, and handles reconnects with backoff when
// needed.
//
// Listen() orchestrates the lifecycle: it watches pq.Listener events,
// triggers reconnects via a small buffered channel, applies exponential
// backoff when creating new listeners fails, and ensures notifications
// that arrive while we are rebuilding the listener are intentionally
// dropped.
//
// Callers only need to pass a context, channel name, and callback; Listen
// hides the IAM token refresh, reconnection, and backoff mechanics.
//
// This is the path a postgres notification takes:
// Postgres NOTIFY
//    -> pq.Listener internals
//    -> listener.Notify channel
//    -> Listen loop
//    -> callback(notification)
//    -> your business logic

func listenerEventToString(t pq.ListenerEventType) string {
	switch t {
	case pq.ListenerEventConnected:
		return "connected"
	case pq.ListenerEventDisconnected:
		return "disconnected"
	case pq.ListenerEventReconnected:
		return "reconnected"
	case pq.ListenerEventConnectionAttemptFailed:
		return "connection failed"
	default:
		return fmt.Sprintf("Unknown: (%d)", t)
	}
}

// Listen subscribes to a Postgres LISTEN channel (pgChannelName) and invokes callback for
// each notification. It automatically reconnects with backoff and pings periodically to
// surface dead sockets. If an onClose callback is provided, it is called once when the
// listener goroutine exits.
//
// Notifications that arrive while the listener is being rebuilt are intentionally dropped.
//
// The callback is invoked from the listener goroutine; it MUST NOT block
// for long periods. If you need to do heavy work, offload it to another
// goroutine.
func Listen(ctx context.Context, conn *PostgresqlConnector, pgChannelName string, callback func(*pq.Notification), onClose func()) error {
	if callback == nil {
		return fmt.Errorf("listener callback cannot be nil")
	}

	reconnectEventCh := make(chan struct{}, 1) // We just need a single reconnect event to trigger, so buffer size of 1

	makeListener := func() (*pq.Listener, error) {
		url, err := conn.GetConnectionString(ctx)
		if err != nil {
			return nil, fmt.Errorf("get url: %w", err)
		}

		cb := func(t pq.ListenerEventType, e error) {
			eventType := listenerEventToString(t)
			log.Printf("Postgres listener (%s): %s (err=%v)", pgChannelName, eventType, e)
			if t == pq.ListenerEventDisconnected || t == pq.ListenerEventConnectionAttemptFailed {
				select {
				case reconnectEventCh <- struct{}{}:
				default:
				}
			}
		}

		listener := pq.NewListener(url, time.Second, 30*time.Second, cb)
		if err := listener.Listen(pgChannelName); err != nil {
			_ = listener.Close()
			return nil, fmt.Errorf("listen %q: %w", pgChannelName, err)
		}
		return listener, nil
	}

	// Build the first listener eagerly so callers learn about init failures immediately.
	listener, err := makeListener()
	if err != nil {
		return err
	}

	go func() {
		defer func() {
			log.Printf("Postgres listener (%s): shutting down (ctx err=%v)", pgChannelName, ctx.Err())
			if onClose != nil {
				onClose()
			}
			if listener != nil {
				_ = listener.Close()
			}
		}()

		backoff := time.Second
		const maxBackoff = 30 * time.Second

		ping := time.NewTicker(60 * time.Second)
		defer ping.Stop()

		for {
			// Rebuild listener with a fresh URL when needed.
			for listener == nil {
				listener, err = makeListener()
				if err != nil {
					log.Printf("listener create failed: %v (retry in %s)", err, backoff)
					select {
					case <-time.After(backoff):
						if backoff < maxBackoff {
							backoff *= 2
							if backoff > maxBackoff {
								backoff = maxBackoff
							}
						}
						continue
					case <-ctx.Done():
						return
					}
				}
				backoff = time.Second // reset on success
			}

			select {
			case n, ok := <-listener.Notify:
				if !ok {
					return
				}
				if n == nil {
					// Seen right after reconnects sometimes.
					continue
				}
				callback(n)

			case <-ping.C:
				// Nudge connection to surface dead sockets.
				go listener.Ping()

			case <-reconnectEventCh:
				// Lib/pq listener has entered a state where we want to reconnect with a new pq.Listener.
				_ = listener.Close()
				listener = nil

			case <-ctx.Done():
				return
			}
		}
	}()

	return nil
}


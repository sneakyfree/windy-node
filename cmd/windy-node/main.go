// windy-node: a small, outbound-only agent that tells Windy Mind what a machine is doing (vitals + AI workloads).
//
//	windy-node enrol --code ABCD-1234   trade the one-time code (shown after you sign in) for a node token
//	windy-node status                   this node's state in Mind (pending until you approve it)
//	windy-node collect --once           print one reading here; nothing is sent
//	windy-node run                      collect every 2 s, report every 15 s (readings wait on disk while offline)
//	windy-node version
//
// It never opens a port, never runs a shell, and never reads process lists or files beyond /proc counters,
// nvidia-smi and the local Ollama /api/ps. A file named `no-commands` in its config dir refuses every command.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sneakyfree/windy-node/internal/collect"
	"github.com/sneakyfree/windy-node/internal/config"
	"github.com/sneakyfree/windy-node/internal/mind"
	"github.com/sneakyfree/windy-node/internal/outbox"
)

// Version is set at build time: -ldflags "-X main.Version=0.1.0".
var Version = "0.1.0-dev"

const (
	tickEvery  = 2 * time.Second
	sendEvery  = 15 * time.Second
	batchMax   = 40
	outboxCap  = 5760 // 24 h of 15 s readings
	maxBackoff = 5 * time.Minute
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch os.Args[1] {
	case "enrol", "enroll":
		err = cmdEnrol(ctx, os.Args[2:])
	case "status":
		err = cmdStatus(ctx)
	case "collect":
		err = cmdCollect(ctx, os.Args[2:])
	case "run":
		err = cmdRun(ctx)
	case "version", "--version", "-v":
		fmt.Println("windy-node", Version)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "windy-node:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: windy-node enrol --code CODE [--label NAME] [--mind URL] | status | collect --once | run | version")
}

func sampler() *collect.Sampler {
	return &collect.Sampler{Root: "/", OllamaURL: collect.OllamaURL(), Version: Version}
}

func cmdEnrol(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("enrol", flag.ExitOnError)
	code := fs.String("code", "", "the one-time code shown after you sign in (10 minutes, single use)")
	label := fs.String("label", "", "a name you'll recognise (default: the hostname)")
	mindURL := fs.String("mind", mind.DefaultURL, "Windy Mind base URL")
	_ = fs.Parse(args)
	if *code == "" {
		return errors.New("--code is required")
	}
	p := config.Default()
	if _, _, err := p.Load(); err == nil {
		return fmt.Errorf("already enrolled (%s); remove it first if you mean to enrol again", p.NodeFile())
	}
	facts := sampler().ReadFacts(ctx, *label)
	if facts.Arch == "" {
		return errors.New("this CPU architecture is not supported yet")
	}
	c := mind.New(*mindURL, Version)
	e, err := c.Enrol(ctx, *code, facts)
	if err != nil {
		return err
	}
	if err := p.Save(config.Node{NodeID: e.NodeID, MindURL: c.BaseURL}, e.NodeToken); err != nil {
		return fmt.Errorf("enrolled as %s but could not save the token: %w", e.NodeID, err)
	}
	fmt.Printf("Enrolled as %s (%s). Approve it in Windy, then run: windy-node run\n", e.NodeID, e.State)
	return nil
}

func cmdStatus(ctx context.Context) error {
	p := config.Default()
	n, tok, err := p.Load()
	if err != nil {
		return err
	}
	me, err := mind.New(n.MindURL, Version).Me(ctx, tok)
	if err != nil {
		return err
	}
	ob := &outbox.Outbox{Path: p.Outbox(), Cap: outboxCap}
	fmt.Printf("node %s: %s\ncommands: %s\nreadings waiting: %d\n", me.NodeID, me.State,
		map[bool]string{true: "allowed", false: "refused (no-commands file present)"}[p.CommandsAllowed()], ob.Len())
	return nil
}

func cmdCollect(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("collect", flag.ExitOnError)
	once := fs.Bool("once", false, "print one reading and exit")
	_ = fs.Parse(args)
	if !*once {
		return errors.New("use --once (run is the long-lived mode)")
	}
	s := sampler()
	s.Tick()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(tickEvery):
	}
	s.Tick()
	r := s.Reading(ctx, tickEvery)
	b, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(b))
	return nil
}

func cmdRun(ctx context.Context) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	p := config.Default()
	n, tok, err := p.Load()
	if err != nil {
		return err
	}
	c := mind.New(n.MindURL, Version)
	ob := &outbox.Outbox{Path: p.Outbox(), Cap: outboxCap}
	s := sampler()
	tick := time.NewTicker(tickEvery)
	defer tick.Stop()
	send := time.NewTicker(sendEvery)
	defer send.Stop()
	backoff, nextTry := sendEvery, time.Time{}
	state := ""
	log.Info("windy-node running", "node", n.NodeID, "mind", n.MindURL, "version", Version)
	s.Tick()
	for {
		select {
		case <-ctx.Done():
			log.Info("windy-node stopping", "waiting", ob.Len())
			return nil
		case <-tick.C:
			s.Tick()
		case <-send.C:
			if err := ob.Add(s.Reading(ctx, sendEvery)); err != nil {
				log.Warn("outbox write failed", "err", err)
			}
			if time.Now().Before(nextTry) {
				continue
			}
			if state != "active" {
				me, err := c.Me(ctx, tok)
				if err != nil {
					var ae *mind.APIError
					if errors.As(err, &ae) && ae.Status == 401 {
						log.Error("node token refused (revoked?): stopping", "node", n.NodeID)
						return err
					}
					backoff, nextTry = grow(backoff), time.Now().Add(backoff)
					log.Warn("mind unreachable; readings kept", "err", err, "retry_in", backoff)
					continue
				}
				if me.State != state {
					log.Info("node state", "state", me.State)
				}
				state = me.State
				if state != "active" {
					wait := time.Duration(max(me.PollAfterS, 15)) * time.Second
					nextTry = time.Now().Add(wait)
					continue
				}
			}
			batch, err := ob.Peek(batchMax)
			if err != nil || len(batch) == 0 {
				continue
			}
			switch err := c.PostReadings(ctx, tok, batch); {
			case err == nil:
				_ = ob.Ack(len(batch))
				backoff = sendEvery
			case errors.Is(err, mind.ErrNotSupported):
				// This Mind has no readings route yet: keep them (capped at 24 h) and look again in 10 min.
				nextTry = time.Now().Add(10 * time.Minute)
				state = ""
			default:
				var ae *mind.APIError
				if errors.As(err, &ae) && ae.Status == 401 {
					log.Error("node token refused (revoked?): stopping", "node", n.NodeID)
					return err
				}
				if errors.As(err, &ae) && ae.Status == 422 {
					_ = ob.Ack(len(batch)) // Mind can never take these; don't retry them forever
					log.Warn("mind rejected readings as invalid; dropped", "n", len(batch), "code", ae.Code)
					continue
				}
				backoff, nextTry = grow(backoff), time.Now().Add(backoff)
				log.Warn("send failed; readings kept", "err", err, "retry_in", backoff)
			}
		}
	}
}

func grow(d time.Duration) time.Duration {
	d *= 2
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}

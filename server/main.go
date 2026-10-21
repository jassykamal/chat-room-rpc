package main

import (
	"chatroom/commons"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	rpc "net/rpc"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// A client that has not polled for this long is treated as disconnected.
const inactiveTimeout = 10 * time.Second

var errNotJoined = errors.New("you are not in the chat room")

// user holds the messages waiting to be picked up by one client.
type user struct {
	inbox    []commons.Message
	lastSeen time.Time
}

// ChatRoom is the object registered with net/rpc. Every exported method with
// the form  func (c *ChatRoom) Name(args T, reply *R) error  becomes a remote
// procedure that clients call as "ChatRoom.Name".
//
// net/rpc runs each incoming call in its own goroutine, so many clients can
// call these methods at the same time. The mutex protects the users map.
type ChatRoom struct {
	mu      sync.Mutex
	users   map[string]*user
	closing bool
}

func NewChatRoom() *ChatRoom {
	return &ChatRoom{users: make(map[string]*user)}
}

// broadcast puts msg in the inbox of every user except `except`.
// The caller must already hold c.mu.
func (c *ChatRoom) broadcast(msg commons.Message, except string) {
	for name, u := range c.users {
		if name != except {
			u.inbox = append(u.inbox, msg)
		}
	}
}

// removeUser deletes a user and tells everyone else. The caller must hold c.mu.
func (c *ChatRoom) removeUser(username, reason string) {
	delete(c.users, username)
	c.broadcast(commons.Message{Text: username + " " + reason}, "")
	log.Printf("%s %s (%d online)", username, reason, len(c.users))
}

// RPC Join: registers a new user. Fails if the username is already taken.
func (c *ChatRoom) Join(username string, ack *bool) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return errors.New("username cannot be empty")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closing {
		return errors.New("server is shutting down")
	}
	if _, taken := c.users[username]; taken {
		return fmt.Errorf("username %q is already taken", username)
	}

	c.users[username] = &user{lastSeen: time.Now()}
	c.broadcast(commons.Message{Text: username + " joined the chat"}, username)
	log.Printf("%s joined (%d online)", username, len(c.users))
	*ack = true
	return nil
}

// RPC Leave: removes the user and notifies the others.
func (c *ChatRoom) Leave(username string, ack *bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.users[username]; !ok {
		return errNotJoined
	}
	c.removeUser(username, "left the chat")
	*ack = true
	return nil
}

// RPC Send: delivers a chat message to everyone except the sender.
func (c *ChatRoom) Send(args *commons.SendArgs, ack *bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	u, ok := c.users[args.Username]
	if !ok {
		return errNotJoined
	}
	u.lastSeen = time.Now()
	c.broadcast(commons.Message{From: args.Username, Text: args.Text}, args.Username)
	*ack = true
	return nil
}

// RPC Poll: returns (and clears) all messages waiting for this user.
// Clients call it every few hundred milliseconds; this is the "polling" approach.
func (c *ChatRoom) Poll(username string, reply *[]commons.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	u, ok := c.users[username]
	if !ok {
		return errNotJoined
	}
	u.lastSeen = time.Now()
	*reply = u.inbox
	u.inbox = nil
	return nil
}

// RPC ListUsers: returns the sorted list of connected usernames.
func (c *ChatRoom) ListUsers(username string, reply *[]string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.users[username]; !ok {
		return errNotJoined
	}
	names := make([]string, 0, len(c.users))
	for name := range c.users {
		names = append(names, name)
	}
	sort.Strings(names)
	*reply = names
	return nil
}

// removeInactive runs in its own goroutine. A client that crashed or lost its
// network stops polling, so after inactiveTimeout we remove it and tell the others.
func (c *ChatRoom) removeInactive() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		for name, u := range c.users {
			if time.Since(u.lastSeen) > inactiveTimeout {
				c.removeUser(name, "disconnected unexpectedly")
			}
		}
		c.mu.Unlock()
	}
}

// startShutdown stops new joins and queues a notice for every connected user.
func (c *ChatRoom) startShutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closing = true
	c.broadcast(commons.Message{Text: "server is shutting down, goodbye!"}, "")
}

func main() {
	addr := flag.String("addr", "0.0.0.0:42586", "address to listen on")
	flag.Parse()

	tcpAddr, err := net.ResolveTCPAddr("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	inbound, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		log.Fatal(err)
	}

	room := NewChatRoom()
	if err := rpc.Register(room); err != nil { // 1. local object for procedures
		log.Fatal(err)
	}
	go rpc.Accept(inbound) // 2. network: serves every client connection in its own goroutine
	go room.removeInactive()

	log.Printf("Chat server listening on %s (press Ctrl+C to stop)", *addr)

	// Block until Ctrl+C (SIGINT) or SIGTERM.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down...")
	room.startShutdown()
	// Wait a little so connected clients can poll once more and see the notice.
	time.Sleep(1500 * time.Millisecond)
	log.Println("Server stopped") // returning from main closes all connections
}

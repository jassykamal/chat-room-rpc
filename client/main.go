package main

import (
	"bufio"
	"chatroom/commons"
	"flag"
	"fmt"
	"log"
	rpc "net/rpc"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// How often the client asks the server for new messages.
const pollInterval = 500 * time.Millisecond

var (
	client   *rpc.Client
	username string

	// done is closed when the user quits, so the polling goroutine knows that
	// errors after that point are expected and should not be reported.
	done     = make(chan struct{})
	quitOnce sync.Once
)

func main() {
	addr := flag.String("addr", "127.0.0.1:42586", "chat server address")
	flag.Parse()

	var err error
	client, err = rpc.Dial("tcp", *addr) // network
	if err != nil {
		log.Fatal("could not connect to the server: ", err)
	}

	in := bufio.NewScanner(os.Stdin)
	join(in)

	// Ctrl+C: leave the room cleanly before exiting.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		fmt.Println()
		quit()
	}()

	// Receive messages in the background while main reads the keyboard.
	go pollMessages()

	printHelp()
	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		switch {
		case line == "":
			continue
		case line == "/quit":
			quit()
		case line == "/users":
			listUsers()
		case line == "/help":
			printHelp()
		case strings.HasPrefix(line, "/"):
			fmt.Println("Unknown command. Type /help to see the commands.")
		default:
			send(line)
		}
	}
	// stdin was closed (e.g. Ctrl+Z / Ctrl+D)
	quit()
}

// join asks for a username until the server accepts one.
func join(in *bufio.Scanner) {
	for {
		fmt.Print("Enter your username: ")
		if !in.Scan() {
			os.Exit(0)
		}
		name := strings.TrimSpace(in.Text())

		var ack bool
		err := client.Call("ChatRoom.Join", name, &ack)
		if err == nil {
			username = name
			fmt.Printf("Welcome to the chat, %s!\n", username)
			return
		}
		// Errors returned by the server method arrive as rpc.ServerError.
		if _, ok := err.(rpc.ServerError); ok {
			fmt.Println("Error:", err)
			continue
		}
		log.Fatal("connection error: ", err)
	}
}

// pollMessages runs in its own goroutine and asks the server for new
// messages every pollInterval. rpc.Client is safe to use from several
// goroutines, so this can run while main is calling Send or ListUsers.
func pollMessages() {
	for {
		var msgs []commons.Message
		err := client.Call("ChatRoom.Poll", username, &msgs)
		if err != nil {
			select {
			case <-done: // we are quitting, ignore the error
				return
			default:
				fmt.Println("*** Lost connection to the server:", err, "***")
				os.Exit(1)
			}
		}

		for _, m := range msgs {
			if m.From == "" {
				fmt.Printf("*** %s ***\n", m.Text)
			} else {
				fmt.Printf("[%s]: %s\n", m.From, m.Text)
			}
		}
		time.Sleep(pollInterval)
	}
}

func send(text string) {
	var ack bool
	args := &commons.SendArgs{Username: username, Text: text}
	if err := client.Call("ChatRoom.Send", args, &ack); err != nil {
		fmt.Println("Could not send message:", err)
	}
}

func listUsers() {
	var users []string
	if err := client.Call("ChatRoom.ListUsers", username, &users); err != nil {
		fmt.Println("Could not get users:", err)
		return
	}
	fmt.Printf("Online users (%d): %s\n", len(users), strings.Join(users, ", "))
}

// quit leaves the room and exits. sync.Once makes it safe to call from both
// the Ctrl+C goroutine and main at the same time.
func quit() {
	quitOnce.Do(func() {
		close(done)
		var ack bool
		client.Call("ChatRoom.Leave", username, &ack) // ignore errors: server may already be gone
		client.Close()
		fmt.Println("You left the chat. Bye!")
		os.Exit(0)
	})
}

func printHelp() {
	fmt.Println("Commands:")
	fmt.Println("  <text>   send a message to everyone")
	fmt.Println("  /users   list connected users")
	fmt.Println("  /help    show this help")
	fmt.Println("  /quit    leave the chat (Ctrl+C also works)")
}

# Chat Room: Client–Server Communication Using RPC

A concurrent terminal chat application written in Go. A central **server** manages
the chat room, and any number of **clients** (each a separate process / terminal)
connect to it using Go's built-in RPC package `net/rpc`. Only the Go standard library is used.

## Features

- Join with a **unique username** (duplicate names are rejected and you are asked again)
- Messages are **broadcast to all other clients** (not back to the sender)
- **Join / leave notifications** are sent to everyone else
- `/users` lists the connected users
- Incoming messages and notifications appear **automatically** in the terminal
- **Unexpected disconnections** (killed client, lost network) are detected and announced
- **Clean shutdown with Ctrl+C** on both the server and the client

## Chosen approach: Polling

The course sections only cover request/reply RPC calls (`client.Call(...)`), with
no server-to-client callbacks, so this project uses **Polling**:

1. Every client starts a background goroutine that calls `ChatRoom.Poll` every 500 ms.
2. The server keeps an **inbox** (a slice of messages) for each connected user.
   When someone sends a message or joins/leaves, the server appends it to the
   inbox of every other user.
3. `Poll` returns the waiting messages and empties the inbox, and the client prints them.

Because every client polls regularly, the server also uses polling to detect crashed
clients: if a user has not polled for 10 seconds, the server removes them and sends
"`<name> disconnected unexpectedly`" to the others.

## Project structure

```
chat-room-rpc/
├── go.mod
├── commons/types.go   # types shared by server and client (Message, SendArgs)
├── server/main.go     # the RPC server
└── client/main.go     # the terminal client
```

### RPC methods (on the server's `ChatRoom` object)

| Method              | Args                | Reply                | Purpose                                  |
|---------------------|---------------------|----------------------|------------------------------------------|
| `ChatRoom.Join`     | `string` username   | `bool`               | Join; fails if the name is taken         |
| `ChatRoom.Send`     | `commons.SendArgs`  | `bool`               | Broadcast a message to all other users   |
| `ChatRoom.Poll`     | `string` username   | `[]commons.Message`  | Get (and clear) waiting messages         |
| `ChatRoom.ListUsers`| `string` username   | `[]string`           | List connected users                     |
| `ChatRoom.Leave`    | `string` username   | `bool`               | Leave the room                           |

### Concurrency

- `net/rpc` handles each client connection, and each call, in its own **goroutine**,
  so many clients can call the server at the same time.
- All shared server state (the `users` map and inboxes) is protected by a **`sync.Mutex`**.
- The server runs a background goroutine (`removeInactive`) that removes clients that stopped polling.
- The client runs a polling goroutine while the main goroutine reads the keyboard.
  `rpc.Client` is safe for concurrent use. A `sync.Once` makes sure leaving
  happens only once, even if `/quit` and Ctrl+C happen together.

## How to run

Requires Go 1.21 or newer. Run all commands from the project root.

**1. Start the server** (terminal 1):

```bash
go run ./server
```

**2. Start as many clients as you like**, one per terminal (terminal 2, 3, ...):

```bash
go run ./client
```

Both programs accept an `-addr` flag. The server listens on `0.0.0.0:42586` and the
client connects to `127.0.0.1:42586` by default. For example, to connect to a server on another machine:

```bash
go run ./client -addr 192.168.1.10:42586
```

Or build binaries first:

```bash
go build -o bin/ ./server ./client
./bin/server        # Windows: .\bin\server.exe
./bin/client        # Windows: .\bin\client.exe
```

## Client commands

| Command   | What it does                               |
|-----------|--------------------------------------------|
| `<text>`  | Send a message to everyone else            |
| `/users`  | List connected users                       |
| `/help`   | Show the command list                      |
| `/quit`   | Leave the chat and exit                    |
| `Ctrl+C`  | Same as `/quit` (leaves cleanly)           |

## Example session

```
Enter your username: alice
Error: username "alice" is already taken
Enter your username: bob
Welcome to the chat, bob!
[alice]: hello everyone
hi alice
/users
Online users (3): alice, bob, carol
*** carol disconnected unexpectedly ***
*** server is shutting down, goodbye! ***
*** Lost connection to the server: connection is shut down ***
```

## Shutdown and error handling

- **Client Ctrl+C or `/quit`**: calls `ChatRoom.Leave`, so the others see "`<name> left the chat`", then exits.
- **Client crash or lost network**: the server stops receiving polls, and after 10 s it
  removes the user and notifies the others.
- **Server Ctrl+C**: stops accepting new joins, queues "server is shutting down" for every
  client, waits 1.5 s so clients can poll it, then exits. The clients detect that the connection
  closed and exit.
- **Server not running**: the client prints "could not connect to the server" and exits.

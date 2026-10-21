package commons

// Types shared by the server and the client. net/rpc encodes them with gob,
// so every field must be exported (start with a capital letter).

// Message is one item waiting in a user's inbox on the server.
// From is empty for server notifications (join/leave/shutdown).
type Message struct {
	From string
	Text string
}

// SendArgs are the arguments of the ChatRoom.Send RPC.
type SendArgs struct {
	Username string
	Text     string
}

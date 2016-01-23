package main

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

// player is a single player, including its connection for the video stream.
type player struct {
	*sync.Mutex
	id     int          // Unique identifier
	active bool         // If the player is playing or not
	conn   *net.TCPConn // Socket connection for video streaming
}

// playerPair are two players that are set to work together. They will stream
// camera input to each other.
type playerPair struct {
	p1 *player
	p2 *player
}

// getParter returns the partner of the given player, or null if none exist.
func getPartner(pp *[]playerPair, index *player) *player {
	for _, val := range *pp {
		if val.p1 == index {
			return val.p2
		} else if val.p2 == index {
			return val.p1
		}
	}

	return nil
}

// assignPartner tries to find an unpaired player to pair the given player to.
// If none exist, the player will be put into a group on its own.
func assignPartner(pp *[]playerPair, p *player) bool {
	// Search for players without a partner
	for _, val := range *pp {
		val.p1.Lock()
		val.p2.Lock()
		defer val.p1.Unlock()
		defer val.p2.Unlock()
		if (val.p1 != nil && val.p1.active) && (val.p1 == nil || !val.p2.active) {
			val.p2 = p
			return true
		} else if (val.p1 == nil || !val.p1.active) && (val.p2 != nil && val.p2.active) {
			val.p1 = p
			return true
		}
	}

	// Put the player on their own
	*pp = append(*pp, playerPair{p, nil})
	return false
}

// listen starts listening for a video connection on a socket for the given
// player. This video will be streamed to the partner.
func listen(p *player, partner *player) {
	addr, err := net.ResolveTCPAddr("tcp", ":8000")
	if err != nil {
		panic(err)
	}
	log.Println("started listening for a connection")
	ln, err := net.ListenTCP("tcp", addr)
	if err != nil {
		panic(err)
	}
	ln.SetDeadline(time.Now().Add(time.Second * 5))

	for {
		p.Lock()
		p.conn, err = ln.AcceptTCP()
		if err != nil {
			log.Println("Socket err:", err)
			p.Unlock()
			continue
		}

		log.Println("connected to player", p.id)
		p.conn.SetKeepAlive(true)
		p.conn.SetKeepAlivePeriod(time.Second / 2)
		p.Unlock()
		streamVideo(p, partner)
		p.Lock()
		log.Println("lost connection to player", p.id)
		p.Unlock()
	}
}

// streamVideo starts streaming video data between players.
func streamVideo(src *player, dest *player) {
}

// jsonError creates a JSON structure with the given error message.
func jsonError(err error) []byte {
	resp := struct {
		Error string `json: "error"`
	}{
		Error: err.Error(),
	}

	data, err := json.Marshal(resp)
	if err != nil {
		panic(err)
	}

	return data
}

func main() {
	players := [2]*player{
		{id: 1, active: false},
		{id: 2, active: false},
	}

	pairs := make([]playerPair, 0, 1)

	http.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		post := func(w http.ResponseWriter, r *http.Request) {
			log.Println("recieved a player join request")

			// Find a non-existant player to assign the joiner to
			var p *player
			var id int
			for _, val := range players {
				val.Lock()
				if !val.active {
					p = val
					id = p.id
					val.Unlock()
					break
				}
				val.Unlock()
			}
			// If there are no available player slots, send an error
			if id == 0 {
				w.Write(jsonError(errors.New("maximum amount of players reached")))
				return
			}

			// Read the request body
			body, err := ioutil.ReadAll(r.Body)
			if err != nil {
				w.Write(jsonError(err))
				return
			}
			defer r.Body.Close()
			// Decode the request body JSON
			var data map[string]interface{}
			err = json.Unmarshal(body, &data)
			if err != nil {
				w.Write(jsonError(err))
				return
			}

			log.Println("accepted join request for player", id)

			// Give the player a partner if possible
			assigned := assignPartner(&pairs, p)
			if assigned {
				log.Println("player", id, "was assigned with player", getPartner(&pairs, p))
			} else {
				log.Println("player", id, "was assigned to their own group")
			}

			// Wait for a video stream
			go listen(p, getPartner(&pairs, p))

			// Construct the response
			resp := struct {
				ID int `json: "id"`
			}{
				ID: id,
			}

			// Convert the response to JSON and send
			out, err := json.Marshal(resp)
			if err != nil {
				w.Write(jsonError(err))
				return
			}
			w.Write(out)
		}

		del := func(w http.ResponseWriter, r *http.Request) {
			// Get the request body
			body, err := ioutil.ReadAll(r.Body)
			if err != nil {
				w.Write(jsonError(err))
				return
			}
			defer r.Body.Close()

			// Unmarshal the response
			var info map[string]interface{}
			err = json.Unmarshal(body, &info)
			if err != nil {
				w.Write(jsonError(err))
				return
			}

			// Get the ID field
			var ok bool
			var id int
			if id, ok = info["id"].(int); !ok {
				w.Write(jsonError(errors.New("id expected in request body")))
				return
			}

			// Find the player with the ID and deregister them
			deleted := false
			for _, val := range players {
				val.Lock()
				if val.id == id && val.active {
					val.active = false
					deleted = true
					val.Unlock()
					break
				}
				val.Unlock()
			}

			// If the user was not found, complain
			if !deleted {
				w.Write(jsonError(errors.New("no active player with that id")))
				return
			}

			// Construct the response
			resp := struct {
				ID int `json: "id"`
			}{
				ID: id,
			}

			// Marshal the response and send it
			data, err := json.Marshal(resp)
			if err != nil {
				w.Write(jsonError(err))
				return
			}
			w.Write(data)
		}

		// Take different actions based on the request type
		switch r.Method {
		case "POST":
			post(w, r)
		case "DELETE":
			del(w, r)
		default:
			w.Write(jsonError(errors.New("requests must either be POSTs or DELETEs")))
		}
	})

	log.Fatalln(http.ListenAndServe(":3000", nil))
}

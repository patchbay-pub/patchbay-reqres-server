package main

import (
        "fmt"
        "log"
        "net/http"
        "io"
        "sync"
        "math/rand"
        "time"
        "strings"
        "strconv"
)

const RequestPrefix = "Patchbay-Request-"
const ResponsePrefix = "Patchbay-Response-"

type PatchedRequest struct {
        httpRequest *http.Request
        responseChan chan PatchedReponse
}

type PatchedReponse struct {
        //body io.ReadCloser
        serverRequest *http.Request
        doneSignal chan struct{}
}

func main() {

        log.Println("Starting up")
        rand.Seed(time.Now().Unix())

	channels := make(map[string]chan PatchedRequest)
	mutex := &sync.Mutex{}

	handler := func(w http.ResponseWriter, r *http.Request) {

		query := r.URL.Query()

                isServer := query.Get("server") == "true"

                mutex.Lock()
                _, ok := channels[r.URL.Path]
                if !ok {
                        channels[r.URL.Path] = make(chan PatchedRequest)
                }
                channel := channels[r.URL.Path]
                mutex.Unlock()

                if isServer {

                        log.Println("server connection")

                        select {
                        case request := <-channel:


                                for k, vList := range request.httpRequest.Header {
                                        for _, v := range vList {
                                                w.Header().Add(RequestPrefix + k, v)
                                        }
                                }

                                doubleClutch := query.Get("doubleclutch")

                                // not all HTTP clients can read the response headers before sending the request body.
                                // "Double clutching" splits the transaction across 2 requests, providing the server with
                                // a random channel for the second request, and connecting that to the original client
                                if doubleClutch == "true" {
                                        // TODO: keep generating until we're sure we have an unused channel. Extremely
                                        // unlikely but you never know.
                                        randomChannelId := genRandomChannelId()
                                        w.Header().Add("Patchbay-Doubleclutch-Channel", randomChannelId)
                                        curlCmd := fmt.Sprintf("curl localhost:9001%s?server=true -d \"YOLO\"\n", randomChannelId)
                                        w.Header().Add("Patchbay-Doubleclutch-Curl-Cmd", curlCmd)

                                        io.Copy(w, request.httpRequest.Body)

                                        ch := make(chan PatchedRequest, 1)

                                        mutex.Lock()
                                        channels[randomChannelId] = ch
                                        mutex.Unlock()

                                        ch <- request
                                } else {

                                        io.Copy(w, request.httpRequest.Body)

                                        doneSignal := make(chan struct{})
                                        response := PatchedReponse{serverRequest: r, doneSignal: doneSignal}

                                        request.responseChan <- response

                                        <-doneSignal
                                }
                        case <-r.Context().Done():
                                log.Println("server canceled")
                        }
                } else {

                        log.Println("client connection")

                        responseChan := make(chan PatchedReponse)
                        request := PatchedRequest{httpRequest: r, responseChan: responseChan}

                        select {
                        case channel <- request:

                                response := <-responseChan

                                var status int
                                for k, vList := range response.serverRequest.Header {
                                        if strings.HasPrefix(k, ResponsePrefix) {
                                                // strip the prefix
                                                headerName := k[len(ResponsePrefix):]
                                                for _, v := range vList {
                                                        w.Header().Add(headerName, v)
                                                }
                                        } else if strings.HasPrefix(k, "Patchbay-Status") {
                                                var err error
                                                status, err = strconv.Atoi(vList[0])
                                                if err != nil {
                                                        log.Fatal(err)
                                                }
                                        }
                                }

                                if status != 0 {
                                        w.WriteHeader(status)
                                }

                                go func() {
                                        <-r.Context().Done()
                                        fmt.Println("cancello")
                                        response.serverRequest.Body.Close()
                                }()

                                io.Copy(w, response.serverRequest.Body)
                                close(response.doneSignal)

                        case <-r.Context().Done():
                                log.Println("client canceled")
                        }
                }
        }

        err := http.ListenAndServe(":9001", http.HandlerFunc(handler))
        if err != nil {
                log.Fatal(err)
        }
}


const channelChars string = "0123456789abcdefghijkmnpqrstuvwxyz";
func genRandomChannelId() string {
        channelId := ""
        for i := 0; i < 32; i++ {
                channelId += string(channelChars[rand.Intn(len(channelChars))])
        }
        return "/" + channelId
}

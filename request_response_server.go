package pbreqres

import (
        "fmt"
        "log"
        "net/http"
        "io"
        "io/ioutil"
        "sync"
        "strings"
        "strconv"
)

const RequestPrefix = "Pb-Req-"
const ResponsePrefix = "Pb-Res-"

type PatchedRequest struct {
        httpRequest *http.Request
        responseChan chan PatchedReponse
}

type PatchedReponse struct {
        //body io.ReadCloser
        serverRequest *http.Request
        doneSignal chan struct{}
}

type RequestResponseServer struct {
        Handle func(w http.ResponseWriter, r *http.Request)
}


func NewRequestResponseServer() *RequestResponseServer {

	channels := make(map[string]chan PatchedRequest)
	mutex := &sync.Mutex{}

        server := &RequestResponseServer{}

	server.Handle = func(w http.ResponseWriter, r *http.Request) {

		query := r.URL.Query()

                isResponder := query.Get("responder") == "true"

                mutex.Lock()
                _, ok := channels[r.URL.Path]
                if !ok {
                        channels[r.URL.Path] = make(chan PatchedRequest)
                }
                channel := channels[r.URL.Path]
                mutex.Unlock()

                if isResponder {

                        log.Println("responder connection")

                        switchChannel := query.Get("switch") == "true"

                        // not all HTTP clients can read the response headers before sending the request body.
                        // switching splits the transaction across 2 requests, providing the responder 
                        // with a random channel for the second request, and connecting that to the original
                        // requester. These are referred to as "double clutch" requests. 
                        var switchChannelIdStr string
                        if switchChannel {
                                switchChannelId, err := ioutil.ReadAll(r.Body)
                                if err != nil {
                                        log.Fatal(err)
                                }
                                switchChannelIdStr = string(switchChannelId)

                                if switchChannelIdStr == "" {
                                        w.WriteHeader(400)
                                        w.Write([]byte("No switching channel provided"))
                                        return
                                }

                                switchChannelIdStr = "/" + switchChannelIdStr
                        }


                        select {
                        case request := <-channel:


                                for k, vList := range request.httpRequest.Header {
                                        for _, v := range vList {
                                                w.Header().Add(RequestPrefix + k, v)
                                        }
                                }

                                // not all HTTP clients can read the response headers before sending the request body.
                                // switching splits the transaction across 2 requests, providing the responder 
                                // with a random channel for the second request, and connecting that to the original
                                // requester. These are referred to as "double clutch" requests. 
                                if switchChannel {

                                        io.Copy(w, request.httpRequest.Body)

                                        ch := make(chan PatchedRequest, 1)

                                        mutex.Lock()
                                        channels[switchChannelIdStr] = ch
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
                                log.Println("responder canceled")
                        }
                } else {

                        log.Println("requester connection")

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
                                        } else if strings.HasPrefix(k, "Pb-Status") {
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
                                        fmt.Println("requester canceled after connection")
                                        response.serverRequest.Body.Close()
                                }()

                                io.Copy(w, response.serverRequest.Body)
                                close(response.doneSignal)

                        case <-r.Context().Done():
                                log.Println("requester canceled before connection")
                        }
                }
        }

        return server
}

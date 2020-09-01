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
        "mime"
        "path"
)

const RequestPrefix = "Pb-H-"
const ResponsePrefix = "Pb-H-"

type PatchedRequest struct {
        path string
        httpRequest *http.Request
        responseChan chan PatchedReponse
}

type PatchedReponse struct {
        //body io.ReadCloser
        responderRequest *http.Request
        doneSignal chan struct{}
}

type RequestResponseServer struct {
	channels map[string]chan PatchedRequest
        mutex *sync.Mutex
}


func NewRequestResponseServer() *RequestResponseServer {

	channels := make(map[string]chan PatchedRequest)
	mutex := &sync.Mutex{}

        server := &RequestResponseServer{channels, mutex}

        return server
}

func (s *RequestResponseServer) Handle(w http.ResponseWriter, r *http.Request) {
        query := r.URL.Query()


        var channelId string

        pathParts := strings.Split(r.URL.Path, "/")

        if len(pathParts) < 2 {
                w.WriteHeader(400)
                w.Write([]byte("Invalid channel. Must start with '/'"))
                return
        }

        protocol := pathParts[1]

        isResponder := false

        if protocol == "res" {
                isResponder = true

                if len(pathParts) < 3 {
                        w.WriteHeader(400)
                        w.Write([]byte("Invalid responder channel. Must be of the form /res/<channel-id>"))
                        return
                }

                channelId = pathParts[2]
        } else {
                if len(pathParts) < 2 {
                        w.WriteHeader(400)
                        w.Write([]byte("Invalid requester channel. Must be of the form /<channel-id>"))
                        return
                }

                channelId = pathParts[1]
        }

        s.mutex.Lock()
        _, ok := s.channels[channelId]
        if !ok {
                s.channels[channelId] = make(chan PatchedRequest)
        }
        channel := s.channels[channelId]
        s.mutex.Unlock()

        fmt.Println("channels", s.channels)

        fmt.Println("channelId", channelId)

        if isResponder {

                log.Println("responder connection")

                if r.Header.Get("Origin") != "" {
                        w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
                        w.Header().Set("Vary", "Origin")
                } else {
                        w.Header().Set("Access-Control-Allow-Origin", "*")
                }

                w.Header().Set("Access-Control-Allow-Headers", "*")
                w.Header().Set("Access-Control-Expose-Headers", "*, Authorization")
                w.Header().Set("Access-Control-Allow-Credentials", "true")

                if r.Method == "OPTIONS" {
                        w.WriteHeader(200)
                        return
                }

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

                        //switchChannelIdStr = "/" + switchChannelIdStr
                }


                select {
                case request := <-channel:

                        w.Header().Add("Pb-Path", request.path)
                        w.Header().Add("Pb-Method", request.httpRequest.Method)

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

                                fmt.Println("switchChannelIdStr", switchChannelIdStr)
                                s.mutex.Lock()
                                s.channels[switchChannelIdStr] = ch
                                s.mutex.Unlock()

                                fmt.Println("switched it to", switchChannelIdStr)

                                ch <- request
                        } else {

                                doneSignal := make(chan struct{})
                                response := PatchedReponse{responderRequest: r, doneSignal: doneSignal}

                                contentType := mime.TypeByExtension(path.Ext(r.URL.Path))
                                if contentType != "" {
                                        response.responderRequest.Header.Set(ResponsePrefix + "Content-Type", contentType)
                                }

                                io.Copy(w, request.httpRequest.Body)

                                request.responseChan <- response

                                <-doneSignal
                        }
                case <-r.Context().Done():
                        log.Println("responder canceled")
                }
        } else {

                log.Println("requester connection")

                responseChan := make(chan PatchedReponse)
                reqPath := r.URL.RequestURI()
                path := reqPath[len("/" + channelId):]
                request := PatchedRequest{path: path, httpRequest: r, responseChan: responseChan}

                select {
                case channel <- request:

                        response := <-responseChan

                        var status int
                        for k, vList := range response.responderRequest.Header {
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
                                response.responderRequest.Body.Close()
                        }()

                        io.Copy(w, response.responderRequest.Body)
                        close(response.doneSignal)

                case <-r.Context().Done():
                        log.Println("requester canceled before connection")
                }
        }
}

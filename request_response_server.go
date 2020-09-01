package pbreqres

import (
        "fmt"
        "log"
        "net/http"
        "io"
        "io/ioutil"
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
        conduitManager *ConduitManager
}


func NewRequestResponseServer() *RequestResponseServer {

        conduitManager := NewConduitManager()

        server := &RequestResponseServer{conduitManager}

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


                // not all HTTP clients can read the response headers before sending the request body.
                // switching splits the transaction across 2 requests, providing the responder 
                // with a random channel for the second request, and connecting that to the original
                // requester. These are referred to as "double clutch" requests. 
                switchChannel := query.Get("switch") == "true"
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
                }


                success, request := s.conduitManager.Respond(channelId, r.Context())

                if success {

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

                                fmt.Println("switched it to", switchChannelIdStr)

                                // TODO: This may be a race condition, but I don't think so. The problem would be if the
                                // second request of the double-clutch came in before this goroutine runs. But in that
                                // case it should just block so I think we're fine. But keep an eye on it.
                                go s.conduitManager.Switch(switchChannelIdStr, request)
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
                } else {
                        log.Println("responder canceled")
                }
        } else {

                log.Println("requester connection")

                responseChan := make(chan PatchedReponse)
                reqPath := r.URL.RequestURI()
                path := reqPath[len("/" + channelId):]
                request := PatchedRequest{path: path, httpRequest: r, responseChan: responseChan}

                success := s.conduitManager.Request(channelId, request, r.Context())

                if success {

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

                } else {
                        log.Println("requester canceled before connection")
                }
        }
}

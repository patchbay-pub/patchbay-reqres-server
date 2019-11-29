package main

import (
        "log"
        "net/http"
)

func main() {
        log.Println("Starting up")

        server := NewRequestResponseServer()
        err := http.ListenAndServe(":9001", http.HandlerFunc(server.Handle))
        if err != nil {
                log.Fatal(err)
        }
}

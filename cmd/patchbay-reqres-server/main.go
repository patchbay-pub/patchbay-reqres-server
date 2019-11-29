package main

import (
        "log"
        "net/http"
        pbreqres "github.com/anderspitman/patchbay-reqres-server"
)

func main() {
        log.Println("Starting up")

        server := pbreqres.NewRequestResponseServer()
        err := http.ListenAndServe(":9001", http.HandlerFunc(server.Handle))
        if err != nil {
                log.Fatal(err)
        }
}

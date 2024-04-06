package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	requestURL := "http://127.0.0.1:9999/read/1"

	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		fmt.Printf("could not create request: %s\n", err)
		os.Exit(1)
	}

	client := http.Client{
		Timeout: 3 * time.Second,
	}

	fmt.Println("started making requests")

	for {
		time.Sleep(time.Millisecond * 5)

		res, err := client.Do(req)
		if err != nil {
			fmt.Printf("failed making http request: %s\n", err)
			continue
		}

		if res.StatusCode != 200 {
			fmt.Printf("failed status code: %d\n", res.StatusCode)
			continue
		}

		resBody, err := io.ReadAll(res.Body)
		if err != nil {
			fmt.Printf("failed to read response body: %s\n", err)
			continue
		}

		fmt.Printf("client: response body: %s\n", resBody)
	}
}

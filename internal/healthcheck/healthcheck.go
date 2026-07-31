package healthcheck

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

func MaybeRun(args []string) {
	if len(args) != 3 || args[1] != "healthcheck" {
		return
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		fmt.Fprintln(os.Stderr, response.Status)
		os.Exit(1)
	}
	os.Exit(0)
}

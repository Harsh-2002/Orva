package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
)

var invokeCmd = &cobra.Command{
	Use:   "invoke [name-or-id]",
	Short: "Invoke a function",
	Long:  "Invoke a deployed function and print the response.",
	Args:  cobra.ExactArgs(1),
	Run:   runInvoke,
}

func init() {
	invokeCmd.Flags().String("data", "{}", "JSON payload to send")
	rootCmd.AddCommand(invokeCmd)
}

func runInvoke(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	nameOrID := args[0]
	fnID := resolveFunctionID(client, nameOrID)
	dataStr, _ := cmd.Flags().GetString("data")

	// Parse data to validate JSON.
	var payload any
	if err := json.Unmarshal([]byte(dataStr), &payload); err != nil {
		exitError("invalid JSON data: %v", err)
	}

	start := time.Now()
	resp, err := client.Post("/fn/"+fnID, payload)
	duration := time.Since(start)
	if err != nil {
		exitError("invoke failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		exitError("read response: %v", err)
	}

	fmt.Printf("Status:   %d\n", resp.StatusCode)
	fmt.Printf("Duration: %s\n", duration.Round(time.Millisecond))
	fmt.Printf("Response:\n")

	// Try to pretty-print JSON.
	var parsed any
	if json.Unmarshal(body, &parsed) == nil {
		pretty, _ := json.MarshalIndent(parsed, "", "  ")
		fmt.Println(string(pretty))
	} else {
		fmt.Println(string(body))
	}
}

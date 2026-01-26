//go:build ignore
// +build ignore

package main

import (
	"fmt"
	"log"

	"github.com/formbricks/hub/pkg/formbricks"
)

func main() {
	// Create client with a dummy API key (replace with your actual API key)
	client := formbricks.NewClient("fbk_your_api_key_here")

	// Get responses
	responses, err := client.GetResponses(formbricks.GetResponsesOptions{
		SurveyID: "cmkv114zm8gspad01m5fowk9u",
	})
	if err != nil {
		log.Fatalf("Error getting responses: %v", err)
	}

	// Print results
	fmt.Printf("Found %d response(s)\n\n", len(responses.Data))

	for i, response := range responses.Data {
		fmt.Printf("Response #%d:\n", i+1)
		fmt.Printf("  ID: %s\n", response.ID)
		fmt.Printf("  Survey ID: %s\n", response.SurveyID)
		fmt.Printf("  Finished: %v\n", response.Finished)
		fmt.Printf("  Created: %s\n", response.CreatedAt.Format("2006-01-02 15:04:05"))
		fmt.Printf("  Updated: %s\n", response.UpdatedAt.Format("2006-01-02 15:04:05"))
		fmt.Printf("  Display ID: %s\n", response.DisplayID)
		fmt.Printf("  Country: %s\n", response.Meta.Country)
		fmt.Printf("  Browser: %s on %s (%s)\n", response.Meta.UserAgent.Browser, response.Meta.UserAgent.OS, response.Meta.UserAgent.Device)
		fmt.Printf("  Data: %v\n", response.Data)
		fmt.Println()
	}
}

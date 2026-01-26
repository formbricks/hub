# Formbricks Go SDK

A simple Go SDK for interacting with the Formbricks API.

## Installation

```go
import "github.com/formbricks/hub/pkg/formbricks"
```

## Usage

### Create a Client

#### Simple client with default settings

```go
client := formbricks.NewClient("your-api-key-here")
```

#### Client with custom base URL

```go
client := formbricks.NewClientWithBaseURL("https://custom.formbricks.com/api/v2", "your-api-key-here")
```

#### Client with full configuration options

```go
client := formbricks.NewClientWithOptions(formbricks.ClientOptions{
    APIKey:   "your-api-key-here",
    BaseURL:  "https://app.formbricks.com/api/v2", // Optional, defaults to production URL
    RetryMax: 5,                                    // Optional, defaults to 3
    Timeout:  60 * time.Second,                    // Optional, defaults to 30 seconds
})
```

### Get Survey Responses

```go
responses, err := client.GetResponses(formbricks.GetResponsesOptions{
    SurveyID: "cmkv114zm8gspad01m5fowk9u",
})
if err != nil {
    log.Fatal(err)
}

for _, response := range responses.Data {
    fmt.Printf("Response ID: %s\n", response.ID)
    fmt.Printf("Finished: %v\n", response.Finished)
    fmt.Printf("Data: %v\n", response.Data)
}
```

## Models

The SDK provides the following models:

- `ResponsesResponse` - The main response wrapper containing an array of responses
- `Response` - A single survey response with all its data, metadata, and timing information
- `Meta` - Metadata about the response (URL, country, user agent)
- `UserAgent` - Browser/device information

## Features

- **Automatic retries**: Uses [go-retryablehttp](https://github.com/hashicorp/go-retryablehttp) for automatic retries with exponential backoff on connection errors and 5xx responses
- **Configurable base URL**: Easily point to different Formbricks instances (production, staging, self-hosted)
- **Type-safe models**: Full struct definitions for all API responses

## API

### Client Constructors

- `NewClient(apiKey string) *Client` - Creates a client with default settings
- `NewClientWithBaseURL(baseURL, apiKey string) *Client` - Creates a client with custom base URL
- `NewClientWithOptions(opts ClientOptions) *Client` - Creates a client with full configuration

### Client Methods

- `GetResponses(opts GetResponsesOptions) (*ResponsesResponse, error)` - Retrieves survey responses

### Options

- `ClientOptions` - Configuration for the client
  - `BaseURL` (string) - Base URL for the API (default: "https://app.formbricks.com/api/v2")
  - `APIKey` (string) - Formbricks API key (required)
  - `RetryMax` (int) - Maximum number of retries (default: 3)
  - `Timeout` (time.Duration) - HTTP client timeout (default: 30 seconds)

- `GetResponsesOptions` - Options for getting responses
  - `SurveyID` (string) - The survey ID to filter responses by

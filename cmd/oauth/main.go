// Command oauth is a one-time helper to obtain a Google OAuth refresh token for
// a personal Gmail account. Run it locally, complete the browser consent, and
// store the printed refresh token as GOOGLE_OAUTH_REFRESH_TOKEN (k8s Secret).
//
// Usage:
//
//	GOOGLE_OAUTH_CLIENT_ID=... GOOGLE_OAUTH_CLIENT_SECRET=... go run ./cmd/oauth
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/o-ga09/adk-go-sample/internal/config"
	googleapi "github.com/o-ga09/adk-go-sample/internal/google"
	"golang.org/x/oauth2"
)

func main() {
	c := config.Load()
	if c.OAuthClientID == "" || c.OAuthClientSecret == "" {
		log.Fatal("set GOOGLE_OAUTH_CLIENT_ID and GOOGLE_OAUTH_CLIENT_SECRET")
	}
	ctx := context.Background()
	oauthCfg := googleapi.OAuthConfig(c)

	codeCh := make(chan string, 1)
	srv := &http.Server{Addr: ":8080"}
	http.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		if _, err := fmt.Fprintln(w, "Authorization received. You can close this tab and return to the terminal."); err != nil {
			log.Printf("callback response: %v", err)
		}
		codeCh <- code
	})

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("callback server: %v", err)
		}
	}()

	authURL := oauthCfg.AuthCodeURL("state-token",
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"))
	fmt.Println("1) Open this URL in your browser and grant access:")
	fmt.Println()
	fmt.Println("  ", authURL)
	fmt.Println()
	fmt.Println("2) Waiting for the redirect to http://localhost:8080/callback ...")

	code := <-codeCh
	_ = srv.Shutdown(ctx)

	tok, err := oauthCfg.Exchange(ctx, code)
	if err != nil {
		log.Fatalf("token exchange failed: %v", err)
	}
	if tok.RefreshToken == "" {
		log.Fatal("no refresh token returned; revoke prior access at https://myaccount.google.com/permissions and retry")
	}
	fmt.Println()
	fmt.Println("SUCCESS. Set this as GOOGLE_OAUTH_REFRESH_TOKEN:")
	fmt.Println()
	fmt.Println(tok.RefreshToken)
}

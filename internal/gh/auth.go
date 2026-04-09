package gh

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// MintJWT creates a signed JWT for the given GitHub App, suitable for
// authenticating as the app (not an installation).
func MintJWT(appID int64, privateKey *rsa.PrivateKey) (string, error) {
	now := time.Now().Unix() - 60
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payloadJSON := fmt.Sprintf(`{"iat":%d,"exp":%d,"iss":"%d"}`, now, now+600, appID)
	payload := base64.RawURLEncoding.EncodeToString([]byte(payloadJSON))
	unsigned := header + "." + payload

	sum := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func MintBotToken(client *http.Client, appID int64, installationID int64, privateKey *rsa.PrivateKey) (string, error) {
	jwt, err := MintJWT(appID, privateKey)
	if err != nil {
		return "", err
	}
	return mintTokenWithJWT(client, jwt, installationID)
}

// MintBotTokenForOwner resolves the installation dynamically by listing the
// app's installations and matching the given repo owner, then mints a token.
func MintBotTokenForOwner(client *http.Client, appID int64, privateKey *rsa.PrivateKey, owner string) (string, error) {
	jwt, err := MintJWT(appID, privateKey)
	if err != nil {
		return "", err
	}
	installID, err := findInstallation(client, jwt, owner)
	if err != nil {
		return "", err
	}
	return mintTokenWithJWT(client, jwt, installID)
}

func mintTokenWithJWT(client *http.Client, jwt string, installationID int64) (string, error) {
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", installationID), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "runoq-runtime")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("token request failed: %s", resp.Status)
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.Token, nil
}

// findInstallation lists the app's installations and returns the ID whose
// account login matches owner (case-insensitive).
func findInstallation(client *http.Client, jwt string, owner string) (int64, error) {
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/app/installations", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "runoq-runtime")

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("list installations failed: %s", resp.Status)
	}

	var installations []struct {
		ID      int64 `json:"id"`
		Account struct {
			Login string `json:"login"`
		} `json:"account"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&installations); err != nil {
		return 0, fmt.Errorf("parse installations: %w", err)
	}

	target := strings.ToLower(owner)
	for _, inst := range installations {
		if strings.ToLower(inst.Account.Login) == target {
			return inst.ID, nil
		}
	}
	return 0, fmt.Errorf("no installation found for owner %q", owner)
}

func LoadPrivateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("invalid PEM private key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("PKCS8 key is not RSA")
	}
	return key, nil
}

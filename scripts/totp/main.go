// Command totp prints the current TOTP code for a base32 secret. It is a
// development/debugging aid for testing the MFA endpoints without a phone.
//
//	go run ./scripts/totp <base32-secret>
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/pquerna/otp/totp"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/totp <base32-secret>")
		os.Exit(2)
	}
	code, err := totp.GenerateCode(os.Args[1], time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println(code)
}

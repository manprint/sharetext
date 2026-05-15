// bcrypt-hash prints the bcrypt hash of the password given on argv. Use the
// output as the value of ADMIN_PASS_HASH.
package main

import (
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: bcrypt-hash <password>")
		os.Exit(2)
	}
	out, err := bcrypt.GenerateFromPassword([]byte(os.Args[1]), 12)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}

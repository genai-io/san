//go:build !unix

package main

import (
	"fmt"
	"os"
)

func acpStdio(*os.File) (*os.File, error) {
	return nil, fmt.Errorf("ACP stdio currently requires Unix pollable pipes")
}

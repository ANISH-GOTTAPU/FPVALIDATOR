package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
    if len(os.Args) < 2 {
        fmt.Println("Usage: go run main.go <path>")
        return
    }

    root := os.Args[1]
    var errs []string

    // Rule 20: check .proto files for full URL + bug ID
    errs = append(errs, checkProtoFiles(root)...)

    info, err := os.Stat(root)
    if err != nil {
        fmt.Println("Invalid path:", err)
        return
    }

    if info.IsDir() {
        _ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
            if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
                return nil
            }
            validateGoFile(path, &errs)
            return nil
        })
    } else {
        if strings.HasSuffix(root, ".go") {
            validateGoFile(root, &errs)
        } else {
            fmt.Println("Provided file is not a .go file")
            return
        }
    }

    if len(errs) > 0 {
        fmt.Println("Validation failed:")
        for _, e := range errs {
            fmt.Println(" -", e)
        }
        os.Exit(1)
    }
    fmt.Println("All validation checks passed ✅")
}


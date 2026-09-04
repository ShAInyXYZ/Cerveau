package main

import (
	"cerveau/internal/tools"
	"context"
	"encoding/json"
	"fmt"
)

func main() {
	b := tools.NewBash("/tmp")
	r, _ := json.Marshal(map[string]string{"command": "python3 -m http.server 9998 &"})
	out, err := b.Execute(context.Background(), r)
	fmt.Println("out:", out)
	fmt.Println("err:", err)
}

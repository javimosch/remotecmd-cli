package main

import (
	"encoding/json"
	"fmt"
)

func emitProgress(event string, data map[string]interface{}) {
	progress := map[string]interface{}{
		"event": event,
		"data":  data,
	}
	jsonBytes, _ := json.Marshal(progress)
	fmt.Println(string(jsonBytes))
}

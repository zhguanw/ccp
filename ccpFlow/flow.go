package ccpFlow

import (
	"fmt"
	"time"

	"ccp/ipc"
)

type DropEvent string

var Isolated DropEvent = DropEvent("isolated")
var Complete DropEvent = DropEvent("complete")

type Flow interface {
	Name() string
	Create(sockid uint32, send ipc.SendOnly)
	Ack(ack uint32, rtt time.Duration)
	Drop(event DropEvent)
}

// name of flow to function which returns blank instance
var flowRegistry map[string]func() Flow // TODO call it protocol registry

// Register a new type of flow
// name: unique name of the flow type
// f: function which returns a blank instance of an implementing type
func Register(name string, f func() Flow) error {
	if flowRegistry == nil {
		flowRegistry = make(map[string]func() Flow)
	}

	if _, ok := flowRegistry[name]; ok {
		return fmt.Errorf("flow algorithm %v already registered", name)
	}
	flowRegistry[name] = f
	return nil
}

func ListRegistered() (regs []string) {
	for name, _ := range flowRegistry {
		regs = append(regs, name)
	}
	return
}

func GetFlow(name string) (Flow, error) {
	if f, ok := flowRegistry[name]; !ok {
		return nil, fmt.Errorf("unknown flow algorithm %v", name)
	} else {
		return f(), nil
	}
}

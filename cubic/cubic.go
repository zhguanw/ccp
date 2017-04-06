package cubic

//Pseudo Code from https://pdfs.semanticscholar.org/4e8f/00ffc07c77340ba4121b36585754f8b8379a.pdf

import (
	"math"
	"time"

	"ccp/ccpFlow"
	"ccp/ipc"

	log "github.com/Sirupsen/logrus"
)

const pktSize = 1460
const initCwnd = 5

// implement ccpFlow.Flow interface
type Cubic struct {
	// state
	cwnd    float32
	lastAck uint32
	sockid  uint32
	ipc     ipc.SendOnly

	//state for cubic
	ssthresh         float32
	cwnd_cnt         float32
	tcp_friendliness bool
	BETA             float32
	fast_convergence bool
	C                float32
	Wlast_max        float32
	epoch_start      float32
	origin_point     float32
	dMin             float32
	Wtcp             float32
	K                float32
	ack_cnt          float32
	cnt              float32
}

func (c *Cubic) Name() string {
	return "cubic"
}

func (c *Cubic) Create(socketid uint32, send ipc.SendOnly) {
	c.sockid = socketid
	c.lastAck = 0
	c.ipc = send
	//Pseudo code doesn't specify how to intialize these
	c.cwnd = initCwnd
	c.ssthresh = 100
	//not sure about what this value should be
	c.cwnd_cnt = 0

	c.tcp_friendliness = true
	c.BETA = 0.2
	c.fast_convergence = true
	c.C = 0.4
	c.cubic_reset()
}

func (c *Cubic) cubic_reset() {
	c.Wlast_max = 0
	c.epoch_start = 0
	c.origin_point = 0
	c.dMin = 0
	c.Wtcp = 0
	c.K = 0
	c.ack_cnt = 0
}

func (c *Cubic) Ack(ack uint32, RTT_TD time.Duration) {
	RTT := float32(RTT_TD.Seconds())
	newBytesAcked := float32(ack - c.lastAck)
	no_of_acks := int(newBytesAcked / pktSize)
	for i := 0; i < no_of_acks; i++ {
		if c.dMin <= 0 || RTT < c.dMin {
			c.dMin = RTT
		}
		if c.cwnd <= c.ssthresh {
			c.cwnd = c.cwnd + 1
		} else {
			c.cubic_update()
			if c.cwnd_cnt > c.cnt {
				c.cwnd = c.cwnd + 1
				c.cwnd_cnt = 0
			} else {
				c.cwnd_cnt = c.cwnd_cnt + 1
			}
		}
	}
	// notify increased cwnd
	c.notifyCwnd()

	log.WithFields(log.Fields{
		"gotAck":      ack,
		"currCwnd":    c.cwnd * pktSize,
		"currLastAck": c.lastAck,
		"newlyAcked":  newBytesAcked,
	}).Info("[cubic] got ack")

	c.lastAck = ack
	return
}

func (c *Cubic) Drop(ev ccpFlow.DropEvent) {
	switch ev {
	case ccpFlow.Isolated:
		c.epoch_start = 0
		if c.cwnd < c.Wlast_max && c.fast_convergence {
			c.Wlast_max = c.cwnd * ((2 - c.BETA) / 2)
		} else {
			c.Wlast_max = c.cwnd
		}
		c.cwnd = c.cwnd * (1 - c.BETA)
		c.ssthresh = c.cwnd
	case ccpFlow.Complete:
		c.cwnd = initCwnd
		c.cubic_reset()
	default:
		log.WithFields(log.Fields{
			"event": ev,
		}).Warn("[cubic] unknown drop event type")
		return
	}

	log.WithFields(log.Fields{
		"currCwnd": c.cwnd * pktSize,
		"event":    ev,
	}).Info("[cubic] drop")

	c.notifyCwnd()
}

func (c *Cubic) cubic_update() {
	c.ack_cnt = c.ack_cnt + 1
	if c.epoch_start <= 0 {
		c.epoch_start = float32(time.Now().UnixNano() / 1e9)
		if c.cwnd < c.Wlast_max {
			c.K = float32(math.Pow(float64((c.Wlast_max-c.cwnd)/c.C), 1.0/3.0))
			c.origin_point = c.Wlast_max
		} else {
			c.K = 0
			c.origin_point = c.cwnd
		}
		c.ack_cnt = 1
		c.Wtcp = c.cwnd
	}
	t := float32(time.Now().UnixNano()/1e9) + c.dMin - c.epoch_start
	target := c.origin_point + c.C*((t-c.K)*(t-c.K)*(t-c.K))
	if target > c.cwnd {
		c.cnt = c.cwnd / (target - c.cwnd)
	} else {
		c.cnt = 100 * c.cwnd
	}
	if c.tcp_friendliness {
		c.cubic_tcp_friendliness()
	}
}

func (c *Cubic) cubic_tcp_friendliness() {
	c.Wtcp = c.Wtcp + (((3 * c.BETA) / (2 - c.BETA)) * (c.ack_cnt / c.cwnd))
	c.ack_cnt = 0
	if c.Wtcp > c.cwnd {
		max_cnt := c.cwnd / (c.Wtcp - c.cwnd)
		if c.cnt > max_cnt {
			c.cnt = max_cnt
		}
	}
}

func (c *Cubic) notifyCwnd() {
	err := c.ipc.SendCwndMsg(c.sockid, uint32(c.cwnd*pktSize))
	if err != nil {
		log.WithFields(log.Fields{"cwnd": c.cwnd * pktSize, "name": c.sockid}).Warn(err)
	}
}

func Init() {
	ccpFlow.Register("cubic", func() ccpFlow.Flow {
		return &Cubic{}
	})
}

package udpDataplane

import (
	"fmt"
	"time"

	"github.com/akshayknarayan/udp/packetops"
	log "github.com/sirupsen/logrus"
)

func (sock *Sock) rx() {
	rcvdPkts := make(chan *Packet)
	go func() {
		for {
			select {
			case <-sock.closed:
				return
			default:
			}

			rcvd := &Packet{}
			_, err := packetops.RecvPacket(sock.conn, rcvd)
			if err != nil {
				log.WithFields(log.Fields{"where": "rx"}).Warn(err)
				continue
			}

			rcvdPkts <- rcvd
		}
	}()

	for {
		select {
		case <-sock.closed:
			return
		case rcvd := <-rcvdPkts:
			err := sock.doRx(rcvd)
			if err != nil {
				log.Warn(err)
			}
		}
	}
}

func (sock *Sock) doRx(rcvd *Packet) error {
	if rcvd.Flag == FIN {
		sock.Close()
		return nil
	} else if rcvd.Flag != ACK {
		var flag string
		switch rcvd.Flag {
		case SYN:
			flag = "SYN"
		case SYNACK:
			flag = "SYNACK"
		default:
			flag = "unknown"
		}

		err := fmt.Errorf("connection in unknown state")
		log.WithFields(log.Fields{
			"flag": flag,
			"pkt":  rcvd,
		}).Panic(err)
		return err
	}

	rcvd.Payload = rcvd.Payload[:rcvd.Length]

	sock.mux.Lock()
	sock.handleAck(rcvd)
	sock.handleData(rcvd)
	sock.mux.Unlock()

	return nil
}

// process ack
// sender
func (sock *Sock) handleAck(rcvd *Packet) {
	firstUnacked, err := sock.inFlight.start()
	if err != nil {
		return
	}

	if rcvd.AckNo < firstUnacked {
		return
	}

	lastAcked, rtt, err := sock.inFlight.rcvdPkt(time.Now(), rcvd)
	if err != nil {
		// there were no packets in flight
		// so we got an ack to a packet we didn't send
		log.WithFields(log.Fields{
			"name":         sock.name,
			"firstUnacked": firstUnacked,
			"rcvd.ackno":   rcvd.AckNo,
			"inFlight":     sock.inFlight.order,
		}).Panic("unknown packet")
	}

	if lastAcked == sock.lastAckedSeqNo && sock.nextSeqNo > lastAcked {
		sock.dupAckCnt++
		log.WithFields(log.Fields{
			"name":           sock.name,
			"sock.lastAcked": lastAcked,
			"firstUnacked":   firstUnacked,
			"rcvd.ackno":     rcvd.AckNo,
			"inFlight":       sock.inFlight.order,
			"dupAcks":        sock.dupAckCnt,
			"sack":           rcvd.Sack,
		}).Debug("dup ack")

		if sock.dupAckCnt >= 3 {
			// dupAckCnt >= 3 -> packet drop
			log.WithFields(log.Fields{
				"name":           sock.name,
				"sock.dupAckCnt": sock.dupAckCnt,
				"sock.lastAcked": lastAcked,
			}).Debug("drop detected")
			sock.inFlight.drop(lastAcked, rcvd)
			select {
			case sock.notifyDrops <- notifyDrop{ev: "3xdupack", lastAck: lastAcked}:
			default:
			}
		}

		select {
		case sock.shouldTx <- struct{}{}:
		default:
		}

		return
	} else {
		sock.dupAckCnt = 0
	}

	sock.lastAckedSeqNo = lastAcked
	select {
	case sock.shouldTx <- struct{}{}:
	default:
	}

	log.WithFields(log.Fields{
		"name":       sock.name,
		"lastAcked":  sock.lastAckedSeqNo,
		"rcvd.ackno": rcvd.AckNo,
		"inFlight":   sock.inFlight.order,
		"rtt":        rtt,
	}).Info("new ack")

	select {
	case sock.notifyAcks <- notifyAck{ack: lastAcked, rtt: rtt}:
	default:
	}
}

// process received payload
func (sock *Sock) handleData(rcvd *Packet) {
	if rcvd.Length > 0 && rcvd.SeqNo >= sock.lastAck { // relevant data packet
		if _, ok := sock.rcvWindow.pkts[rcvd.SeqNo]; ok {
			// spurious retransmission
			return
		}

		// new data!
		sock.rcvWindow.addPkt(time.Now(), rcvd)
		ackNo, err := sock.rcvWindow.cumAck(sock.lastAck)
		if err != nil {
			ackNo = sock.lastAck
			log.WithFields(log.Fields{"name": sock.name, "ackNo": ackNo}).Warn(err)
		}

		sock.lastAck = ackNo
		log.WithFields(log.Fields{
			"name":         sock.name,
			"rcvd.seqno":   rcvd.SeqNo,
			"rcvd.length":  rcvd.Length,
			"sock.lastAck": sock.lastAck,
		}).Info("new data")

		copy(sock.readBuf[rcvd.SeqNo:], rcvd.Payload[:rcvd.Length])
		select {
		case sock.shouldPass <- sock.lastAck:
			log.Debug("sent on shouldPass")
		case <-sock.closed:
			close(sock.shouldPass)
			return
		default:
			log.Debug("skipping shouldPass")
		}

		select {
		case sock.shouldTx <- struct{}{}:
		default:
		}
	}
}

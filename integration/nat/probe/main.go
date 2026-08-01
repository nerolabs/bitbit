// Command probe is a standalone TCP hole-punch primitive used to prove — in
// the integration/nat harness, behind real kernel NAT — that two peers on
// separate NATed LANs can form a DIRECT connection (cone NAT) or must fall
// back to a relay (symmetric NAT). It is NOT part of silt; it exists so the
// hole-punch approach (#27) is validated against real conntrack before it is
// wired into the transport.
//
//	probe coord   :9000                 # rendezvous: pairs two peers, swaps their observed addrs
//	probe peer    <coord> <lport> <name>  # binds lport (SO_REUSEPORT), learns its mapped addr, punches
//
// A peer prints "DIRECT-OK <peer>" on a successful direct connection, or
// "DIRECT-FAIL" if the punch didn't establish within the deadline.
package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func reuse(_, _ string, c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		if e := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); e != nil {
			serr = e
			return
		}
		serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	}); err != nil {
		return err
	}
	return serr
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: probe coord :PORT | probe peer COORD LPORT NAME")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "coord":
		coord(os.Args[2])
	case "peer":
		peer(os.Args[2], os.Args[3], os.Args[4])
	default:
		os.Exit(2)
	}
}

// coord accepts exactly two peers, records the source address it OBSERVES for
// each (their NAT mapping), then tells each peer the other's observed address
// and a "go". It keeps both control conns open so the mappings stay alive.
func coord(addr string) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "coord listen:", err)
		os.Exit(1)
	}
	type reg struct {
		conn net.Conn
		obs  string
	}
	var peers []reg
	for len(peers) < 2 {
		c, err := ln.Accept()
		if err != nil {
			os.Exit(1)
		}
		// read the peer's name line (blocks until it registers)
		br := bufio.NewReader(c)
		if _, err := br.ReadString('\n'); err != nil {
			c.Close()
			continue
		}
		peers = append(peers, reg{conn: c, obs: c.RemoteAddr().String()})
		fmt.Printf("coord: peer %d observed at %s\n", len(peers), c.RemoteAddr().String())
	}
	// swap observed addresses + fire the go signal
	fmt.Fprintf(peers[0].conn, "GO %s\n", peers[1].obs)
	fmt.Fprintf(peers[1].conn, "GO %s\n", peers[0].obs)
	time.Sleep(8 * time.Second) // hold the mappings while the peers punch
}

// peer binds lport with SO_REUSEPORT, dials the coordinator FROM lport (so the
// coordinator observes the mapping for lport), learns the other peer's observed
// address, then simultaneously listens on lport and dials the peer from lport —
// the crossing SYNs establish a direct link through a cone NAT.
func peer(coordAddr, lport, name string) {
	local := &net.TCPAddr{Port: atoi(lport)}
	dialer := net.Dialer{LocalAddr: local, Control: reuse, Timeout: 4 * time.Second}

	// 1. reveal our lport mapping to the coordinator.
	cc, err := dialer.Dial("tcp", coordAddr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "peer dial coord:", err)
		fmt.Println("DIRECT-FAIL")
		return
	}
	defer cc.Close()
	fmt.Fprintf(cc, "%s\n", name)
	line, err := bufio.NewReader(cc).ReadString('\n')
	if err != nil {
		fmt.Println("DIRECT-FAIL")
		return
	}
	var peerAddr string
	fmt.Sscanf(line, "GO %s", &peerAddr)
	if peerAddr == "" {
		fmt.Println("DIRECT-FAIL")
		return
	}
	fmt.Fprintf(os.Stderr, "peer %s: punching toward %s from :%s\n", name, peerAddr, lport)

	// 2. listen on lport (REUSEPORT) to catch the peer's crossing SYN...
	lc := net.ListenConfig{Control: reuse}
	ln, err := lc.Listen(context.Background(), "tcp", fmt.Sprintf(":%s", lport))
	if err != nil {
		fmt.Fprintln(os.Stderr, "peer listen:", err)
	}
	got := make(chan net.Conn, 4)
	if ln != nil {
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				got <- c
			}
		}()
	}
	// 3. ...and dial the peer from lport, retrying to keep firing SYNs.
	go func() {
		for i := 0; i < 20; i++ {
			c, err := dialer.Dial("tcp", peerAddr)
			if err == nil {
				got <- c
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
	}()

	select {
	case c := <-got:
		// prove it's bidirectional: swap a byte.
		c.SetDeadline(time.Now().Add(3 * time.Second))
		c.Write([]byte("x"))
		buf := make([]byte, 1)
		c.Read(buf)
		c.Close()
		fmt.Printf("DIRECT-OK %s\n", peerAddr)
	case <-time.After(7 * time.Second):
		fmt.Println("DIRECT-FAIL")
	}
}

func atoi(s string) int {
	n := 0
	fmt.Sscanf(s, "%d", &n)
	return n
}

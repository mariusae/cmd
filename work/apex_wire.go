package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Apex uses postcard-encoded messages in little-endian length-prefixed
// frames. The dashboard only needs the small, stable subset below: attach as
// a tool, receive B3 plumbs, acknowledge them, and propose a Goto. Checking
// the protocol number makes an incompatible Apex fail explicitly rather than
// sending a message it might interpret differently.
const (
	apexWireProtocol      = 8
	apexMaxFrameSize      = 256 << 20
	apexServerBuild       = 0
	apexServerWelcome     = 1
	apexServerApplied     = 4
	apexServerError       = 8
	apexServerPlumb       = 12
	apexClientHello       = 0
	apexClientPlumbAck    = 16
	apexClientPropose     = 20
	apexAttachmentTool    = 1
	apexProposalGoto      = 24
	apexPositionKeep      = 0
	apexExecContextWindow = 0
	apexExecContextColumn = 1
	apexExecContextTop    = 2
)

type apexPlumb struct {
	ID     uint64
	Window string
	Verb   string
	Text   string
	Point  int
	HasAt  bool
}

type apexApplied struct {
	ID  uint64
	Err error
}

type apexWireClient struct {
	conn      net.Conn
	mu        sync.Mutex
	events    chan apexPlumb
	applied   chan apexApplied
	closed    chan struct{}
	closeOnce sync.Once
	nextID    uint64
}

func apexSessionName(getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	if name := getenv("apexsession"); name != "" {
		return name
	}
	if name := getenv("APEX_SESSION"); name != "" {
		return name
	}
	return "default"
}

func apexSocketPath(getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	if path := getenv("APEX_SOCKET"); path != "" {
		return filepath.Clean(path)
	}
	base := getenv("TMPDIR")
	if base == "" {
		base = "/tmp"
	}
	who := getenv("USER")
	if who == "" {
		who = getenv("LOGNAME")
	}
	if who == "" {
		if current, err := user.Current(); err == nil {
			who = current.Username
		}
	}
	if who == "" {
		who = strconv.Itoa(os.Getuid())
	}
	return filepath.Join(base, "apex-"+who, "main.sock")
}

func apexHookLocation(getenv func(string) string) (socket, session, window string) {
	if getenv == nil {
		getenv = os.Getenv
	}
	window = getenv("winid")
	if _, err := strconv.ParseUint(window, 10, 64); err != nil {
		return "", "", ""
	}
	return apexSocketPath(getenv), apexSessionName(getenv), window
}

func connectApexTool(socket, session, name string) (*apexWireClient, error) {
	conn, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connecting to Apex: %w", err)
	}
	client := &apexWireClient{
		conn:    conn,
		events:  make(chan apexPlumb, 8),
		applied: make(chan apexApplied, 8),
		closed:  make(chan struct{}),
		nextID:  1,
	}
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		conn.Close()
		return nil, err
	}
	frame, err := readApexFrame(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("reading Apex handshake: %w", err)
	}
	protocol, err := decodeApexBuild(frame)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if protocol != apexWireProtocol {
		conn.Close()
		return nil, fmt.Errorf("Apex protocol %d is not supported (expected %d)", protocol, apexWireProtocol)
	}
	if err := client.write(apexHelloMessage(session, name)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("attaching Apex dashboard tool: %w", err)
	}
	for {
		frame, err = readApexFrame(conn)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("waiting for Apex dashboard tool: %w", err)
		}
		kind, _, err := readPostcardUint(frame, 0)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("reading Apex handshake: %w", err)
		}
		switch kind {
		case apexServerWelcome:
			if err := conn.SetDeadline(time.Time{}); err != nil {
				conn.Close()
				return nil, err
			}
			go client.readLoop()
			return client, nil
		case apexServerError:
			message, _, decodeErr := readPostcardString(frame, 1)
			conn.Close()
			if decodeErr != nil {
				return nil, fmt.Errorf("Apex refused dashboard tool")
			}
			return nil, errors.New(message)
		}
	}
}

func (a *apexWireClient) Close() error {
	var err error
	a.closeOnce.Do(func() {
		close(a.closed)
		err = a.conn.Close()
	})
	return err
}

func (a *apexWireClient) readLoop() {
	defer close(a.events)
	defer close(a.applied)
	for {
		frame, err := readApexFrame(a.conn)
		if err != nil {
			return
		}
		if plumb, ok := decodeApexPlumb(frame); ok {
			select {
			case a.events <- plumb:
			case <-a.closed:
				return
			}
		}
		if applied, ok := decodeApexApplied(frame); ok {
			select {
			case a.applied <- applied:
			case <-a.closed:
				return
			}
		}
	}
}

func (a *apexWireClient) acknowledge(id uint64, accepted bool) error {
	payload := appendPostcardUint(nil, apexClientPlumbAck)
	payload = appendPostcardUint(payload, id)
	if accepted {
		payload = append(payload, 1)
	} else {
		payload = append(payload, 0)
	}
	return a.write(payload)
}

func (a *apexWireClient) gotoWindow(name string) error {
	a.mu.Lock()
	id := a.nextID
	a.nextID++
	a.mu.Unlock()

	if err := a.write(apexGotoMessage(id, name)); err != nil {
		return err
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case result, ok := <-a.applied:
			if !ok {
				return fmt.Errorf("Apex navigation connection closed")
			}
			if result.ID == id {
				return result.Err
			}
		case <-timer.C:
			return fmt.Errorf("timed out waiting for Apex to navigate")
		case <-a.closed:
			return fmt.Errorf("Apex navigation connection closed")
		}
	}
}

func (a *apexWireClient) write(payload []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return writeApexFrame(a.conn, payload)
}

func apexHelloMessage(session, name string) []byte {
	payload := appendPostcardUint(nil, apexClientHello)
	payload = appendPostcardString(payload, session)
	payload = appendPostcardString(payload, name)
	payload = appendPostcardUint(payload, apexAttachmentTool)
	return append(payload, 0) // no attach script
}

func apexGotoMessage(id uint64, name string) []byte {
	payload := appendPostcardUint(nil, apexClientPropose)
	payload = appendPostcardUint(payload, id)
	payload = appendPostcardUint(payload, apexProposalGoto)
	payload = appendPostcardString(payload, name)
	return appendPostcardUint(payload, apexPositionKeep)
}

func decodeApexBuild(frame []byte) (uint64, error) {
	kind, offset, err := readPostcardUint(frame, 0)
	if err != nil || kind != apexServerBuild {
		return 0, fmt.Errorf("unexpected Apex handshake")
	}
	protocol, _, err := readPostcardUint(frame, offset)
	if err != nil {
		return 0, fmt.Errorf("reading Apex protocol: %w", err)
	}
	return protocol, nil
}

func decodeApexPlumb(frame []byte) (apexPlumb, bool) {
	kind, offset, err := readPostcardUint(frame, 0)
	if err != nil || kind != apexServerPlumb {
		return apexPlumb{}, false
	}
	id, offset, err := readPostcardUint(frame, offset)
	if err != nil {
		return apexPlumb{}, false
	}
	context, offset, err := readPostcardUint(frame, offset)
	if err != nil {
		return apexPlumb{}, false
	}
	var window uint64
	switch context {
	case apexExecContextWindow, apexExecContextColumn:
		window, offset, err = readPostcardUint(frame, offset)
		if err != nil {
			return apexPlumb{}, false
		}
	case apexExecContextTop:
	default:
		return apexPlumb{}, false
	}
	verb, offset, err := readPostcardString(frame, offset)
	if err != nil {
		return apexPlumb{}, false
	}
	text, offset, err := readPostcardString(frame, offset)
	if err != nil {
		return apexPlumb{}, false
	}
	_, offset, err = readPostcardString(frame, offset) // directory
	if err != nil {
		return apexPlumb{}, false
	}
	groupCount, offset, err := readPostcardUint(frame, offset)
	if err != nil || groupCount > 100 {
		return apexPlumb{}, false
	}
	for range groupCount {
		_, offset, err = readPostcardString(frame, offset)
		if err != nil {
			return apexPlumb{}, false
		}
	}
	_, point, hasAt, offset, err := readPostcardSpan(frame, offset)
	if err != nil {
		return apexPlumb{}, false
	}
	_, _, _, _, err = readPostcardSpan(frame, offset) // explicit selection
	if err != nil {
		return apexPlumb{}, false
	}
	return apexPlumb{
		ID:     id,
		Window: strconv.FormatUint(window, 10),
		Verb:   verb,
		Text:   text,
		Point:  point,
		HasAt:  hasAt && context == apexExecContextWindow,
	}, true
}

func decodeApexApplied(frame []byte) (apexApplied, bool) {
	kind, offset, err := readPostcardUint(frame, 0)
	if err != nil || kind != apexServerApplied {
		return apexApplied{}, false
	}
	id, offset, err := readPostcardUint(frame, offset)
	if err != nil {
		return apexApplied{}, false
	}
	result, offset, err := readPostcardUint(frame, offset)
	if err != nil {
		return apexApplied{}, false
	}
	if result == 0 {
		option, _, err := readPostcardUint(frame, offset)
		if err != nil || option > 1 {
			return apexApplied{}, false
		}
		return apexApplied{ID: id}, true
	}
	if result != 1 {
		return apexApplied{}, false
	}
	message, _, err := readPostcardString(frame, offset)
	if err != nil {
		return apexApplied{}, false
	}
	return apexApplied{ID: id, Err: errors.New(message)}, true
}

func readPostcardSpan(data []byte, offset int) (buffer uint64, q0 int, present bool, next int, err error) {
	option, offset, err := readPostcardUint(data, offset)
	if err != nil {
		return 0, 0, false, offset, err
	}
	if option == 0 {
		return 0, 0, false, offset, nil
	}
	if option != 1 {
		return 0, 0, false, offset, fmt.Errorf("invalid postcard option %d", option)
	}
	buffer, offset, err = readPostcardUint(data, offset)
	if err != nil {
		return 0, 0, false, offset, err
	}
	position, offset, err := readPostcardUint(data, offset)
	if err != nil {
		return 0, 0, false, offset, err
	}
	_, offset, err = readPostcardUint(data, offset)
	if err != nil {
		return 0, 0, false, offset, err
	}
	if position > uint64(^uint(0)>>1) {
		return 0, 0, false, offset, fmt.Errorf("postcard position is too large")
	}
	return buffer, int(position), true, offset, nil
}

func appendPostcardString(dst []byte, value string) []byte {
	dst = appendPostcardUint(dst, uint64(len(value)))
	return append(dst, value...)
}

func appendPostcardUint(dst []byte, value uint64) []byte {
	for value >= 0x80 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}

func readPostcardString(data []byte, offset int) (string, int, error) {
	length, offset, err := readPostcardUint(data, offset)
	if err != nil {
		return "", offset, err
	}
	if length > uint64(len(data)-offset) {
		return "", offset, io.ErrUnexpectedEOF
	}
	next := offset + int(length)
	return string(data[offset:next]), next, nil
}

func readPostcardUint(data []byte, offset int) (uint64, int, error) {
	if offset < 0 || offset >= len(data) {
		return 0, offset, io.ErrUnexpectedEOF
	}
	value, size := binary.Uvarint(data[offset:])
	if size <= 0 {
		return 0, offset, fmt.Errorf("invalid postcard integer")
	}
	return value, offset + size, nil
}

func readApexFrame(reader io.Reader) ([]byte, error) {
	var length [4]byte
	if _, err := io.ReadFull(reader, length[:]); err != nil {
		return nil, err
	}
	size := binary.LittleEndian.Uint32(length[:])
	if size > apexMaxFrameSize {
		return nil, fmt.Errorf("Apex frame is too large: %d bytes", size)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func writeApexFrame(writer io.Writer, payload []byte) error {
	if len(payload) > apexMaxFrameSize {
		return fmt.Errorf("Apex frame is too large: %d bytes", len(payload))
	}
	var length [4]byte
	binary.LittleEndian.PutUint32(length[:], uint32(len(payload)))
	if err := writeAll(writer, length[:]); err != nil {
		return err
	}
	return writeAll(writer, payload)
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

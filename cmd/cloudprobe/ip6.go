package main

import (
	"bytes"
	"encoding/binary"
	"errors"
)

func icmpv6Checksum(packet []byte) uint16 {
	if len(packet) < 40 {
		return 0xffff
	}
	var sum uint32
	add := func(data []byte) {
		for len(data) >= 2 {
			sum += uint32(binary.BigEndian.Uint16(data[:2]))
			data = data[2:]
		}
		if len(data) == 1 {
			sum += uint32(data[0]) << 8
		}
	}
	add(packet[8:40])
	payloadLength := uint32(len(packet) - 40)
	sum += payloadLength >> 16
	sum += payloadLength & 0xffff
	sum += 58
	add(packet[40:])
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

func icmpv6EchoReply(packet, registeredAP []byte) ([]byte, bool, error) {
	if len(packet) < 40 || packet[0]>>4 != 6 {
		return nil, false, errors.New("invalid IPv6 packet")
	}
	if len(registeredAP) != 16 || !bytes.Equal(packet[24:40], registeredAP) || packet[6] != 58 {
		return nil, false, nil
	}
	if len(packet) < 48 || int(binary.BigEndian.Uint16(packet[4:6])) != len(packet)-40 {
		return nil, false, errors.New("malformed IPv6 echo request")
	}
	if packet[40] != 128 || packet[41] != 0 {
		return nil, false, nil
	}
	reply := append([]byte(nil), packet...)
	copy(reply[8:24], packet[24:40])
	copy(reply[24:40], packet[8:24])
	reply[40] = 129
	reply[42], reply[43] = 0, 0
	binary.BigEndian.PutUint16(reply[42:44], icmpv6Checksum(reply))
	return reply, true, nil
}

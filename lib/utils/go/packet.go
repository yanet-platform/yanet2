package dataplane

//#cgo CFLAGS: -I../../.. -I../../../lib
//#cgo LDFLAGS: -L../../../build/lib/utils -llib_utils
//#cgo LDFLAGS: -L../../../build/lib/dataplane/packet -lpacket
//
//#include "lib/dataplane/packet/packet.h"
//#include "lib/utils/packet.h"
import "C"
import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

type PacketData struct {
	data       []uint8
	txDeviceId uint16
	rxDeviceId uint16
}

////////////////////////////////////////////////////////////////////////////////

type Packet C.struct_packet

func NewPacketFromData(data PacketData, pinner *runtime.Pinner) (*Packet, error) {
	if pinner != nil {
		pinner.Pin(data.data)
	}

	packet := C.struct_packet{}
	packetData := C.struct_packet_data{
		data:         (*C.uint8_t)(&data.data[0]),
		size:         C.uint16_t(len(data.data)),
		tx_device_id: C.uint16_t(data.txDeviceId),
		rx_device_id: C.uint16_t(data.rxDeviceId),
	}

	rc := C.fill_packet_from_data(&packet, &packetData)
	if rc != 0 {
		return nil, fmt.Errorf("failed to create packet: rc=%d", rc)
	}

	return (*Packet)(&packet), nil
}

func (packet *Packet) Data() PacketData {
	data := C.packet_data((*C.struct_packet)(packet))
	size := data.size
	bytes := unsafe.Slice((*uint8)(data.data), size)
	return PacketData{
		data:       bytes,
		txDeviceId: uint16(data.tx_device_id),
		rxDeviceId: uint16(data.rx_device_id),
	}
}

func (packet *Packet) Info() *framework.PacketInfo {
	data := packet.Data()
	info, err := framework.NewPacketParser().ParsePacket(data.data)
	if err != nil {
		msg := fmt.Sprintf("failed to parse packet: %v", err)
		panic(msg)
	}
	return info
}

////////////////////////////////////////////////////////////////////////////////

type PacketList C.struct_packet_list

type PacketListIter struct {
	packetList    *PacketList
	currentPacket *Packet
}

func (iter *PacketListIter) Next() *Packet {
	packet := iter.currentPacket
	if packet != nil {
		iter.currentPacket = (*Packet)((*C.struct_packet)(packet).next)
	}
	return packet
}

func (packetList *PacketList) Iter() *PacketListIter {
	return &PacketListIter{
		packetList:    packetList,
		currentPacket: (*Packet)((*C.struct_packet_list)(packetList).first),
	}
}

func (packetList *PacketList) Add(packet *Packet) {
	C.packet_list_add((*C.struct_packet_list)(packetList), (*C.struct_packet)(packet))
}

func NewPacketList(packets ...Packet) PacketList {
	packetList := PacketList{}
	C.packet_list_init((*C.struct_packet_list)(&packetList))
	for packetIdx := range packets {
		packet := &packets[packetIdx]
		packetList.Add(packet)
	}
	return packetList
}

func NewPacketListFromData(data ...PacketData) (*PacketList, error) {
	packetList := NewPacketList()
	for idx := range data {
		packet, err := NewPacketFromData(data[idx], nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create new packet from data[%d]: %v", idx, err)
		}
		packetList.Add(packet)
	}
	return &packetList, nil
}

func (packetList *PacketList) Free() {
	C.free_packet_list((*C.struct_packet_list)(packetList))
}

////////////////////////////////////////////////////////////////////////////////

type PacketFront C.struct_packet_front

func (pf *PacketFront) InputList() *PacketList {
	return (*PacketList)(&(*C.struct_packet_front)(pf).input)
}

func (pf *PacketFront) OutputList() *PacketList {
	return (*PacketList)(&(*C.struct_packet_front)(pf).output)
}

func (pf *PacketFront) DropList() *PacketList {
	return (*PacketList)(&(*C.struct_packet_front)(pf).drop)
}

func Free(pf *PacketFront) {
	pf.OutputList().Free()
	pf.InputList().Free()
	pf.DropList().Free()
}

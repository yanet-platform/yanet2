package dataplane

//#cgo CFLAGS: -I../../.. -I../../../lib -fsanitize=address,leak,undefined
//#cgo LDFLAGS: -L../../../build/lib/utils -llib_utils -fsanitize=address,leak,undefined
//#cgo LDFLAGS: -L../../../build/lib/dataplane/packet -lpacket
//
//#include "lib/dataplane/packet/packet.h"
//#include "lib/utils/packet.h"
import "C"
import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/gopacket/gopacket"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

type PacketData struct {
	Payload    []uint8
	TxDeviceId uint16
	RxDeviceId uint16
}

func NewPacketData(txDeviceId uint16, rxDeviceId uint16, packet gopacket.Packet) PacketData {
	return PacketData{Payload: packet.Data(), TxDeviceId: txDeviceId, RxDeviceId: rxDeviceId}
}

func PacketsData(txDeviceId uint16, rxDeviceId uint16, packets ...gopacket.Packet) []PacketData {
	payload := make([]PacketData, 0, len(packets))
	for idx := range packets {
		payload = append(
			payload,
			PacketData{
				Payload:    packets[idx].Data(),
				TxDeviceId: txDeviceId,
				RxDeviceId: rxDeviceId,
			},
		)
	}
	return payload
}

////////////////////////////////////////////////////////////////////////////////

type Packet C.struct_packet

func NewPacketFromData(data PacketData) (*Packet, error) {
	packet := C.struct_packet{}
	packetData := C.struct_packet_data{
		data:         (*C.uint8_t)(&data.Payload[0]),
		size:         C.uint16_t(len(data.Payload)),
		tx_device_id: C.uint16_t(data.TxDeviceId),
		rx_device_id: C.uint16_t(data.RxDeviceId),
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
		Payload:    bytes,
		TxDeviceId: uint16(data.tx_device_id),
		RxDeviceId: uint16(data.rx_device_id),
	}
}

func (packet *Packet) Info() *framework.PacketInfo {
	data := packet.Data()
	info, err := framework.NewPacketParser().ParsePacket(data.Payload)
	if err != nil {
		msg := fmt.Sprintf("failed to parse packet: %v", err)
		panic(msg)
	}
	return info
}

func (packet *Packet) Next() *Packet {
	return (*Packet)((*C.struct_packet)(packet).next)
}

func (packet *Packet) Free() {
	C.free_packet((*C.struct_packet)(packet))
}

////////////////////////////////////////////////////////////////////////////////

type PacketList C.struct_packet_list

func (packetList *PacketList) First() *Packet {
	return (*Packet)((*C.struct_packet_list)(packetList).first)
}

func (packetList *PacketList) Count() int {
	return int((*C.struct_packet_list)(packetList).count)
}

func (packetList *PacketList) Add(packet *Packet) {
	C.packet_list_add((*C.struct_packet_list)(packetList), (*C.struct_packet)(packet))
}

func (packetList *PacketList) Info() []*framework.PacketInfo {
	info := make([]*framework.PacketInfo, 0, packetList.Count())
	packet := packetList.First()
	for {
		if packet == nil {
			break
		}
		info = append(info, packet.Info())
		packet = packet.Next()
	}
	return info
}

func (packetList *PacketList) Data() []PacketData {
	packet := packetList.First()
	data := make([]PacketData, 0, packetList.Count())
	for {
		if packet == nil {
			break
		}
		data = append(data, packet.Data())
		packet = packet.Next()
	}
	return data
}

func NewPacketList(pinner *runtime.Pinner, packets []*Packet) *PacketList {
	packetList := PacketList{}
	pinner.Pin(&packetList)

	C.packet_list_init((*C.struct_packet_list)(&packetList))
	for _, packet := range packets {
		packetList.Add(packet)
	}

	return &packetList
}

func NewPacketListFromPackets(pinner *runtime.Pinner, packets ...gopacket.Packet) (*PacketList, error) {
	data := PacketsData(0, 0, packets...)
	return NewPacketListFromData(pinner, data...)
}

func NewPacketListFromData(pinner *runtime.Pinner, data ...PacketData) (*PacketList, error) {
	packetList := NewPacketList(pinner, make([]*Packet, 0))
	pinner.Pin(packetList)

	for idx := range data {
		packetData := data[idx]
		pinner.Pin(&packetData.Payload[0])
		packet, err := NewPacketFromData(data[idx])
		if err != nil {
			return nil, fmt.Errorf("failed to create new packet from data[%d]: %v", idx, err)
		}
		pinner.Pin(packet)
		packetList.Add(packet)
	}
	return packetList, nil
}

func (packetList *PacketList) Free() {
	C.free_packet_list((*C.struct_packet_list)(packetList))
}

////////////////////////////////////////////////////////////////////////////////

type PacketFront C.struct_packet_front

type PacketFrontPayload struct {
	Output [][]byte
	Input  [][]byte
	Drop   [][]byte
}

func NewPacketFront(
	pinner *runtime.Pinner,
	input *PacketList,
	output *PacketList,
	drop *PacketList,
) *PacketFront {
	packetFront := C.struct_packet_front{}
	pinner.Pin(&packetFront)

	C.packet_front_init(&packetFront)

	if input != nil {
		packetFront.input = *(*C.struct_packet_list)(input)
	}

	if output != nil {
		packetFront.output = *(*C.struct_packet_list)(output)
	}

	if drop != nil {
		packetFront.drop = *(*C.struct_packet_list)(drop)
	}

	return (*PacketFront)(&packetFront)
}

func (pf *PacketFront) InputList() *PacketList {
	return (*PacketList)(&(*C.struct_packet_front)(pf).input)
}

func (pf *PacketFront) OutputList() *PacketList {
	return (*PacketList)(&(*C.struct_packet_front)(pf).output)
}

func (pf *PacketFront) DropList() *PacketList {
	return (*PacketList)(&(*C.struct_packet_front)(pf).drop)
}

func (pf *PacketFront) Payload() PacketFrontPayload {
	raw := func(data []PacketData) [][]byte {
		result := make([][]byte, 0, len(data))
		for idx := range data {
			result = append(result, data[idx].Payload)
		}
		return result
	}
	return PacketFrontPayload{
		Output: raw(pf.OutputList().Data()),
		Input:  raw(pf.InputList().Data()),
		Drop:   raw(pf.DropList().Data()),
	}
}

func (pf *PacketFront) Free() {
	pf.OutputList().Free()
	pf.InputList().Free()
	pf.DropList().Free()
}

func NewPacketFrontFromPackets(pinner *runtime.Pinner, packets ...gopacket.Packet) (*PacketFront, error) {
	packetList, err := NewPacketListFromPackets(pinner, packets...)
	if err != nil {
		return nil, fmt.Errorf("failed to create packet list: %w", err)
	}
	return NewPacketFront(pinner, packetList, nil, nil), nil
}

func NewPacketFrontFromPayload(pinner *runtime.Pinner, payload [][]byte) (*PacketFront, error) {
	packets := []PacketData{}
	for idx := range payload {
		packets = append(packets, PacketData{
			Payload:    payload[idx],
			TxDeviceId: 0,
			RxDeviceId: 0,
		})
	}
	packetList, err := NewPacketListFromData(pinner, packets...)
	if err != nil {
		return nil, fmt.Errorf("failed to create new packet list: %w", err)
	}
	return NewPacketFront(pinner, packetList, nil, nil), nil
}

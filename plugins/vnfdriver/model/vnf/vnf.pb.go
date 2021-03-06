// Code generated by protoc-gen-gogo.
// source: vnf.proto
// DO NOT EDIT!

/*
Package vnf is a generated protocol buffer package.

It is generated from these files:
	vnf.proto

It has these top-level messages:
	VnfEntity
*/
package vnf

import proto "github.com/gogo/protobuf/proto"

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal

type VnfEntity struct {
	Name        string                  `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Container   string                  `protobuf:"bytes,2,opt,name=container,proto3" json:"container,omitempty"`
	L2Xconnects []*VnfEntity_L2XConnect `protobuf:"bytes,3,rep,name=l2xconnects" json:"l2xconnects,omitempty"`
}

func (m *VnfEntity) Reset()         { *m = VnfEntity{} }
func (m *VnfEntity) String() string { return proto.CompactTextString(m) }
func (*VnfEntity) ProtoMessage()    {}

func (m *VnfEntity) GetL2Xconnects() []*VnfEntity_L2XConnect {
	if m != nil {
		return m.L2Xconnects
	}
	return nil
}

type VnfEntity_L2XConnect struct {
	PortLabels []string `protobuf:"bytes,1,rep,name=port_labels" json:"port_labels,omitempty"`
}

func (m *VnfEntity_L2XConnect) Reset()         { *m = VnfEntity_L2XConnect{} }
func (m *VnfEntity_L2XConnect) String() string { return proto.CompactTextString(m) }
func (*VnfEntity_L2XConnect) ProtoMessage()    {}

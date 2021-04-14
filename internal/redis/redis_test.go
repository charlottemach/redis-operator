package redis

import (
	"testing"
)

type slotsTests struct {
	Nodes int
	Slots []*NodesSlots
}

var slots = []slotsTests{
	{
		0, []*NodesSlots{},
	},
	{
		1, []*NodesSlots{
			{Start: 0, End: 16383},
		},
	},
	{
		2, []*NodesSlots{
			{Start: 0, End: 8191},
			{Start: 8192, End: 16383},
		},
	},
	{
		3, []*NodesSlots{
			{Start: 0, End: 5460},
			{Start: 5461, End: 10921},
			{Start: 10922, End: 16383},
		},
	},
	{
		4, []*NodesSlots{
			{Start: 0, End: 4095},
			{Start: 4096, End: 8191},
			{Start: 8192, End: 12287},
			{Start: 12288, End: 16383},
		},
	},
}

func TestSlotsPerNode(t *testing.T) {
	slotsNum, _ := slotsPerNode(3, 16384)
	slotsShouldBe := 5461
	if slotsNum != slotsShouldBe {
		t.Errorf("Slots should be %d", slotsShouldBe)
	}
}

func TestSlotsNode(t *testing.T) {
	for i := range slots {
		nodeSlots := SplitNodeSlots(slots[i].Nodes)
		if len(nodeSlots) != slots[i].Nodes {
			t.Errorf("(seq %d) NodeSlots number should be %d, got %d", i, slots[i].Nodes, len(nodeSlots))
			t.FailNow()
		}
		for j := 0; j < len(slots[i].Slots); j++ {
			if slots[i].Slots[j].Start != nodeSlots[j].Start {
				t.Errorf("Expected sequence %d, Start:%d (got %d), End:%d (got %d)", slots[i].Nodes, slots[i].Slots[j].Start, nodeSlots[j].Start, slots[i].Slots[j].End, nodeSlots[j].End)
			}
		}
	}
}

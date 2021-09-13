package controllers

import (
	"reflect"
	"testing"
)

func TestCalculateSlotMoveSequence(t *testing.T) {
	testTable := map[string]struct {
		startingMap          map[string][]int
		expectedMoveSequence []MoveSequence
	}{
		"simple-move-test": {
			map[string][]int{
				"node-1": makeRange(0, 10),
				"node-2": makeRange(11, 21),
				"node-3": makeRange(22, 23),
			},
			[]MoveSequence{
				{
					From:  "node-1",
					To:    "node-3",
					Slots: []int{8, 9, 10},
				},
				{
					From:  "node-2",
					To:    "node-3",
					Slots: []int{19, 20, 21},
				},
			},
		},
	}

	for name, table := range testTable {
		options := NewMoveMapOptions()
		slotMoveMap := CalculateSlotMoveMap(table.startingMap, options)
		got := CalculateMoveSequence(table.startingMap, slotMoveMap, options)

		if len(got) != len(table.expectedMoveSequence) {
			t.Errorf("Unexpected slot map received from %s iteration. Got %v :: Expected %v.", name, got, table.expectedMoveSequence)
			t.Errorf("Another error")
		}

		for _, gotNode := range got {
			found := false
			for _, expectedNode := range table.expectedMoveSequence {
				if gotNode.From == expectedNode.From && gotNode.To == expectedNode.To {
					found = reflect.DeepEqual(gotNode, expectedNode)
					break
				}
			}
			if !found {
				t.Errorf("Unexpected slot map received from %s iteration. Got %v :: Expected %v.", name, gotNode, table.expectedMoveSequence)
			}
		}
	}
}

func TestCalculateSlotMoveSequenceWithWeights(t *testing.T) {
	testTable := map[string]struct {
		startingMap          map[string][]int
		expectedMoveSequence []MoveSequence
		weights              map[string]int
	}{
		"without-weights": {
			map[string][]int{
				"node-1": makeRange(0, 10),
				"node-2": makeRange(11, 21),
				"node-3": makeRange(22, 23),
			},
			[]MoveSequence{
				{
					From:  "node-1",
					To:    "node-3",
					Slots: []int{8, 9, 10},
				},
				{
					From:  "node-2",
					To:    "node-3",
					Slots: []int{19, 20, 21},
				},
			},
			map[string]int{},
		},
		"with-one-0-weight": {
			map[string][]int{
				"node-1": makeRange(0, 10),
				"node-2": makeRange(11, 21),
				"node-3": makeRange(22, 23),
			},
			[]MoveSequence{
				{
					From:  "node-3",
					To:    "node-1",
					Slots: []int{22},
				},
				{
					From:  "node-3",
					To:    "node-2",
					Slots: []int{23},
				},
			},
			map[string]int{
				"node-3": 0,
			},
		},
		"with-two-0-weights": {
			map[string][]int{
				"node-1": makeRange(0, 10),
				"node-2": makeRange(11, 21),
				"node-3": makeRange(22, 23),
			},
			[]MoveSequence{
				{
					From:  "node-2",
					To:    "node-1",
					Slots: makeRange(11, 21),
				},
				{
					From:  "node-3",
					To:    "node-1",
					Slots: makeRange(22, 23),
				},
			},
			map[string]int{
				"node-2": 0,
				"node-3": 0,
			},
		},
		"with-one-2-weight": {
			map[string][]int{
				"node-1": makeRange(0, 10),
				"node-2": makeRange(11, 21),
				"node-3": makeRange(22, 23),
			},
			[]MoveSequence{
				{
					From:  "node-1",
					To:    "node-3",
					Slots: makeRange(6, 10),
				},
				{
					From:  "node-2",
					To:    "node-3",
					Slots: makeRange(17, 21),
				},
			},
			map[string]int{
				"node-3": 2,
			},
		},
		"with-uneven-numbers-and-0-weight": {
			// To to rounding errors, we could end up with a 0 weighted node, which still has slots after calculation.
			// This test protects that from happening
			map[string][]int{
				"node-1": makeRange(1, 10),
				"node-2": makeRange(11, 20),
				"node-3": makeRange(21, 27),
			},
			[]MoveSequence{
				{
					From:  "node-3",
					To:    "node-1",
					Slots: makeRange(21, 23),
				},
				{
					From:  "node-3",
					To:    "node-2",
					Slots: makeRange(24, 27),
				},
			},
			map[string]int{
				"node-3": 0,
			},
		},
		"with-1-slot-versus-16386-and-0-weight": {
			// To to rounding errors, we could end up with a 0 weighted node, which still has slots after calculation.
			// This test protects that from happening
			map[string][]int{
				"node-1": makeRange(1, 8192),
				"node-2": makeRange(8193, 16383),
				"node-3": makeRange(16384, 16384),
			},
			[]MoveSequence{
				{
					From:  "node-3",
					To:    "node-2",
					Slots: makeRange(16384, 16384),
				},
			},
			map[string]int{
				"node-3": 0,
			},
		},
	}

	for name, table := range testTable {
		options := NewMoveMapOptions()
		options.weights = table.weights
		slotMoveMap := CalculateSlotMoveMap(table.startingMap, options)
		got := CalculateMoveSequence(table.startingMap, slotMoveMap, options)

		if len(got) != len(table.expectedMoveSequence) {
			t.Errorf("Unexpected slot map received from %s iteration. Got %v :: Expected %v.", name, got, table.expectedMoveSequence)
			t.Errorf("Another error")
		}

		for _, gotNode := range got {
			found := false
			for _, expectedNode := range table.expectedMoveSequence {
				if gotNode.From == expectedNode.From && gotNode.To == expectedNode.To {
					found = reflect.DeepEqual(gotNode, expectedNode)
					break
				}
			}
			if !found {
				t.Errorf("Unexpected slot map received from %s iteration. Got %v :: Expected %v.", name, got, table.expectedMoveSequence)
			}
		}
	}
}

func TestSlotMoveMap(t *testing.T) {
	testTable := map[string]struct {
		startingMap     map[string][]int
		expectedMoveMap map[string][]int
	}{
		"simple-move-test": {
			map[string][]int{
				"node-1": makeRange(0, 5461),
				"node-2": makeRange(5462, 16200),
				"node-3": makeRange(16201, 16384),
			},
			map[string][]int{
				"node-1": []int{},
				"node-2": makeRange(10923, 16200),
				"node-3": []int{},
			},
		},
		"empty-node-move-map": {
			map[string][]int{
				"node-1": makeRange(0, 16384),
				"node-2": []int{},
				"node-3": []int{},
			},
			map[string][]int{
				"node-1": makeRange(5461, 16384),
				"node-2": []int{},
				"node-3": []int{},
			},
		},
		"balanced-node-move-map": {
			map[string][]int{
				"node-1": makeRange(0, 5461),
				"node-2": makeRange(5462, 10922),
				"node-3": makeRange(10923, 16384),
			},
			map[string][]int{
				"node-1": []int{},
				"node-2": []int{},
				"node-3": []int{},
			},
		},
		"balanced-node-move-map-within-threshold": {
			map[string][]int{
				"node-1": makeRange(0, 5440),
				"node-2": makeRange(5441, 10922),
				"node-3": makeRange(10923, 16384),
			},
			map[string][]int{
				"node-1": []int{},
				"node-2": []int{},
				"node-3": []int{},
			},
		},
	}

	for name, table := range testTable {
		options := NewMoveMapOptions()
		options.threshhold = 2
		got := CalculateSlotMoveMap(table.startingMap, options)

		if !reflect.DeepEqual(got, table.expectedMoveMap) {
			t.Errorf("Unexpected slot map received from %s iteration. Got %v :: Expected %v.", name, got, table.expectedMoveMap)
		}
	}
}

func TestSlotMoveMapWithWeights(t *testing.T) {
	testTable := map[string]struct {
		startingMap     map[string][]int
		expectedMoveMap map[string][]int
		weights         map[string]int
	}{
		"simple-move-test-without-weights": {
			map[string][]int{
				"node-1": makeRange(0, 5461),
				"node-2": makeRange(5462, 16200),
				"node-3": makeRange(16201, 16384),
			},
			map[string][]int{
				"node-1": []int{},
				"node-2": makeRange(10923, 16200),
				"node-3": []int{},
			},
			map[string]int{},
		},
		"simple-move-test-with-one-0-weight": {
			map[string][]int{
				"node-1": makeRange(0, 5461),
				"node-2": makeRange(5462, 10922),
				"node-3": makeRange(10923, 16384),
			},
			map[string][]int{
				"node-1": []int{},
				"node-2": []int{},
				"node-3": makeRange(10923, 16384),
			},
			map[string]int{
				"node-3": 0,
			},
		},
		"simple-move-test-with-two-0-weights": {
			map[string][]int{
				"node-1": makeRange(0, 5461),
				"node-2": makeRange(5462, 16200),
				"node-3": makeRange(16201, 16384),
			},
			map[string][]int{
				"node-1": []int{},
				"node-2": makeRange(5462, 16200),
				"node-3": makeRange(16201, 16384),
			},
			map[string]int{
				"node-2": 0,
				"node-3": 0,
			},
		},
		"simple-move-test-with-one-2-weights": {
			map[string][]int{
				"node-1": makeRange(0, 5461),
				"node-2": makeRange(5462, 10922),
				"node-3": makeRange(10923, 16384),
			},
			map[string][]int{
				"node-1": makeRange(4096, 5461),
				"node-2": makeRange(9558, 10922),
				"node-3": []int{},
			},
			map[string]int{
				"node-3": 2,
			},
		},
	}

	for name, table := range testTable {
		options := NewMoveMapOptions()
		options.threshhold = 2
		options.weights = table.weights
		got := CalculateSlotMoveMap(table.startingMap, options)

		if !reflect.DeepEqual(got, table.expectedMoveMap) {
			t.Errorf("Unexpected slot map received from %s iteration. Got %v :: Expected %v.", name, got, table.expectedMoveMap)
		}
	}
}

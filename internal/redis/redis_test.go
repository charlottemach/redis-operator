package redis

import (
	"reflect"
	"strings"
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

func TestConfigStringToMap(t *testing.T) {
	type args struct {
		config string
	}
	tests := []struct {
		name string
		args args
		want map[string]string
	}{
		{
			"single-entry", args{`maxmemory 500mb`},
			map[string]string{"maxmemory": "500mb"},
		},
		{
			"whitespace-around", args{`

							maxmemory 500mb
							maxmemory-samples 5
							slaveof 127.0.0.1 6380

							`,
			},
			map[string]string{"maxmemory": "500mb", "maxmemory-samples": "5", "slaveof": "127.0.0.1 6380"},
		},
		{
			"whitespace-between", args{`maxmemory    500mb
							maxmemory-samples 5`,
			},
			map[string]string{"maxmemory": "500mb", "maxmemory-samples": "5"},
		},
		{
			"module-load", args{`
							module load /usr/local/lib.so
							maxmemory-samples 5`,
			},
			map[string]string{"maxmemory-samples": "5", "module": "load /usr/local/lib.so"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConfigStringToMap(tt.args.config); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ConfigStringToMap() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMergeWithDefaultConfig(t *testing.T) {
	type args struct {
		custom map[string]string
	}
	tests := []struct {
		name string
		args args
		want map[string]string
	}{
		{
			"forbidden-override",
			args{map[string]string{"maxmemory": "2gb", "cluster-enabled": "no"}},
			map[string]string{"maxmemory": "2gb", "cluster-enabled": "yes"},
		},
		{
			"defaults-not-set",
			args{map[string]string{}},
			map[string]string{"maxmemory": "1600mb", "cluster-enabled": "yes"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeWithDefaultConfig(tt.args.custom)
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("MergeWithDefaultConfig() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestConvertRedisMemToMbytes(t *testing.T) {
	type args struct {
		maxMemory string
	}
	tests := []struct {
		name    string
		args    args
		want    int
		wantErr bool
	}{
		{"mb", args{maxMemory: "300mb"}, 300, false},
		{"m", args{maxMemory: "300m"}, 300, false},
		{"kb", args{maxMemory: "3000kb"}, 2, false},
		{"gb", args{maxMemory: "5gb"}, 5120, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ConvertRedisMemToMbytes(tt.args.maxMemory)
			if (err != nil) != tt.wantErr {
				t.Errorf("ConvertRedisMemToMbytes() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ConvertRedisMemToMbytes() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMapToConfigString(t *testing.T) {
	type args struct {
		config map[string]string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{"one", args{config: map[string]string{"moduleload": "/usr/lib/m.so"}}, "moduleload /usr/lib/m.so"},
		{"two", args{config: map[string]string{"moduleload": "/usr/lib/m.so", "maxmemory": "500mb"}},
			`moduleload /usr/lib/m.so
maxmemory 500mb`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MapToConfigString(tt.args.config); strings.TrimSpace(got) != tt.want {
				t.Errorf("MapToConfigString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStateParser(t *testing.T) {
	tests := []struct {
		name string
		args string
		want map[string]string
	}{
		{"test_conf_empty", "", map[string]string{}},
		{"test_conf_3_lines", `
cluster_state:ok
cluster_slots_ok:16384
cluster_slots_pfail:0
`, map[string]string{"cluster_state": "ok", "cluster_slots_ok": "16384", "cluster_slots_pfail": "0"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetClusterInfo(tt.args); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MapToConfigString() = %v, want %v", got, tt.want)
			}
		})
	}
}

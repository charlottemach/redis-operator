package redis

import (
	"context"
	"fmt"
	redisclient "github.com/go-redis/redis/v8"
	"github.com/sirupsen/logrus"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	UnusedHashSlot = iota
	NewHashSlot
	AssignedHashSlot
)

///////////////////////////////////////////////////////////
// detail info for redis node.
type NodeInfo struct {
	host string
	port uint

	name       string
	addr       string
	flags      []string
	replicate  string
	pingSent   int
	pingRecv   int
	weight     int
	balance    int
	linkStatus string
	slots      []int
	migrating  map[int]string
	importing  map[int]string
}

func (self *NodeInfo) HasFlag(flag string) bool {
	for _, f := range self.flags {
		if strings.Contains(f, flag) {
			return true
		}
	}
	return false
}

func (self *NodeInfo) String() string {
	return fmt.Sprintf("%s:%d", self.host, self.port)
}

//////////////////////////////////////////////////////////
// struct of redis cluster node.
type ClusterNode struct {
	r             *redisclient.Client
	info          *NodeInfo
	dirty         bool
	friends       []*NodeInfo
	replicasNodes []*ClusterNode
	verbose       bool
	ctx           context.Context
}

func NewClusterNode(addr string) (node *ClusterNode) {
	var host, port string
	var err error

	hostport := strings.Split(addr, "@")[0]
	parts := strings.Split(hostport, ":")
	if len(parts) < 2 {
		logrus.Fatalf("Invalid IP or Port (given as %s) - use IP:Port format", addr)
		return nil
	}

	if len(parts) > 2 {
		// ipv6 in golang must like: "[fe80::1%lo0]:53", see detail in net/dial.go
		host, port, err = net.SplitHostPort(hostport)
		if err != nil {
			logrus.Fatalf("New cluster node error: %s!", err)
		}
	} else {
		host = parts[0]
		port = parts[1]
	}

	p, _ := strconv.ParseUint(port, 10, 0)
	node = &ClusterNode{
		r: nil,
		info: &NodeInfo{
			host:      host,
			port:      uint(p),
			slots:     make([]int, 0),
			migrating: make(map[int]string),
			importing: make(map[int]string),
			replicate: "",
		},
		dirty:   false,
		verbose: false,
	}

	if os.Getenv("ENV_MODE_VERBOSE") != "" {
		node.verbose = true
	}

	err = node.Connect()
	if err != nil {
		logrus.Fatalf("Invalid IP or Port (given as %s) - use IP:Port format", err)
	}

	return node
}

func (clusterNode *ClusterNode) Host() string {
	return clusterNode.info.host
}

func (clusterNode *ClusterNode) Port() uint {
	return clusterNode.info.port
}

func (clusterNode *ClusterNode) Name() string {
	return clusterNode.info.name
}

func (clusterNode *ClusterNode) HasFlag(flag string) bool {
	for _, f := range clusterNode.info.flags {
		if strings.Contains(f, flag) {
			return true
		}
	}
	return false
}

func (clusterNode *ClusterNode) Replicate() string {
	return clusterNode.info.replicate
}

func (clusterNode *ClusterNode) SetReplicate(nodeId string) {
	clusterNode.info.replicate = nodeId
	clusterNode.dirty = true
}

func (clusterNode *ClusterNode) Weight() int {
	return clusterNode.info.weight
}

func (clusterNode *ClusterNode) SetWeight(w int) {
	clusterNode.info.weight = w
}

func (clusterNode *ClusterNode) Balance() int {
	return clusterNode.info.balance
}

func (clusterNode *ClusterNode) SetBalance(balance int) {
	clusterNode.info.balance = balance
}

func (clusterNode *ClusterNode) Slots() []int {
	return clusterNode.info.slots
}

func (clusterNode *ClusterNode) Migrating() map[int]string {
	return clusterNode.info.migrating
}

func (clusterNode *ClusterNode) Importing() map[int]string {
	return clusterNode.info.importing
}

func (clusterNode *ClusterNode) R() *redisclient.Client {
	return clusterNode.r
}

func (clusterNode *ClusterNode) Info() *NodeInfo {
	return clusterNode.info
}

func (clusterNode *ClusterNode) IsDirty() bool {
	return clusterNode.dirty
}

func (clusterNode *ClusterNode) Friends() []*NodeInfo {
	return clusterNode.friends
}

func (clusterNode *ClusterNode) ReplicasNodes() []*ClusterNode {
	return clusterNode.replicasNodes
}

func (clusterNode *ClusterNode) AddReplicasNode(node *ClusterNode) {
	clusterNode.replicasNodes = append(clusterNode.replicasNodes, node)
}

func (clusterNode *ClusterNode) String() string {
	return clusterNode.info.String()
}

func (clusterNode *ClusterNode) NodeString() string {
	return clusterNode.info.String()
}

func (clusterNode *ClusterNode) Connect() error {
	if clusterNode.r != nil {
		return nil
	}
	r := redisclient.NewClient(&redisclient.Options{
		Addr:     fmt.Sprintf("%s:%d", clusterNode.Host(), clusterNode.Port()),
		Password: "",
		DB:       0,
	})

	err := r.Ping(context.TODO()).Err()
	if err != nil {
		return err
	}

	clusterNode.r = r

	return nil
}

func (clusterNode *ClusterNode) Call(args ...interface{}) *redisclient.Cmd {
	err := clusterNode.Connect()
	if err != nil {
		return nil
	}

	return clusterNode.r.Do(context.TODO(), args...)
}

func (clusterNode *ClusterNode) Dbsize() (int, error) {
	return clusterNode.Call("DBSIZE").Int()
}

func (clusterNode *ClusterNode) ClusterAddNode(addr string) (ret string, err error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" || port == "" {
		return "", fmt.Errorf("Bad format of host:port: %s!", addr)
	}
	return clusterNode.Call("CLUSTER", "meet", host, port).Text()
}

func (clusterNode *ClusterNode) ClusterReplicateWithNodeID(nodeid string) (ret string, err error) {
	return clusterNode.Call("CLUSTER", "replicate", nodeid).Text()
}

func (clusterNode *ClusterNode) ClusterForgetNodeID(nodeid string) (ret string, err error) {
	return clusterNode.Call("CLUSTER", "forget", nodeid).Text()
}

func (clusterNode *ClusterNode) ClusterNodeShutdown() (err error) {
	clusterNode.r.Shutdown(clusterNode.ctx)
	return nil
}

func (clusterNode *ClusterNode) ClusterCountKeysInSlot(slot int) (int, error) {
	return clusterNode.Call("CLUSTER", "countkeysinslot", slot).Int()
}

func (clusterNode *ClusterNode) ClusterGetKeysInSlot(slot int, count int) ([]string, error) {
	return clusterNode.R().ClusterGetKeysInSlot(clusterNode.ctx, slot, count).Result()
}

func (clusterNode *ClusterNode) ClusterSetSlot(slot int, cmd string) (string, error) {
	return clusterNode.Call("CLUSTER", "setslot", slot, cmd, clusterNode.Name()).Text()
}

func (clusterNode *ClusterNode) AssertCluster() bool {
	info, err := clusterNode.Call("INFO", "cluster").Text()
	if err != nil ||
		!strings.Contains(info, "cluster_enabled:1") {
		return false
	}

	return true
}

func (clusterNode *ClusterNode) AssertEmpty() bool {

	info, err := clusterNode.Call("CLUSTER", "INFO").Text()
	db0, e := clusterNode.Call("INFO", "db0").Text()
	if err != nil || !strings.Contains(info, "cluster_known_nodes:1") ||
		e != nil || strings.Trim(db0, " ") != "" {
		logrus.Fatalf("Node %s is not empty. Either the node already knows other nodes (check with CLUSTER NODES) or contains some key in database 0.", clusterNode.String())
	}

	return true
}

func (clusterNode *ClusterNode) LoadInfo(getfriends bool) (err error) {
	var result string
	if result, err = clusterNode.Call("CLUSTER", "NODES").Text(); err != nil {
		return err
	}
	nodes := strings.Split(result, "\n")
	for _, val := range nodes {
		// name addr flags role ping_sent ping_recv link_status slots
		parts := strings.Split(val, " ")
		if len(parts) <= 3 {
			continue
		}

		sent, _ := strconv.ParseInt(parts[4], 0, 32)
		recv, _ := strconv.ParseInt(parts[5], 0, 32)
		addr := strings.Split(parts[1], "@")[0]
		host, port, _ := net.SplitHostPort(addr)
		p, _ := strconv.ParseUint(port, 10, 0)

		node := &NodeInfo{
			name:       parts[0],
			addr:       parts[1],
			flags:      strings.Split(parts[2], ","),
			replicate:  parts[3],
			pingSent:   int(sent),
			pingRecv:   int(recv),
			linkStatus: parts[6],

			host:      host,
			port:      uint(p),
			slots:     make([]int, 0),
			migrating: make(map[int]string),
			importing: make(map[int]string),
		}

		if parts[3] == "-" {
			node.replicate = ""
		}

		if strings.Contains(parts[2], "myself") {
			if clusterNode.info != nil {
				clusterNode.info.name = node.name
				clusterNode.info.addr = node.addr
				clusterNode.info.flags = node.flags
				clusterNode.info.replicate = node.replicate
				clusterNode.info.pingSent = node.pingSent
				clusterNode.info.pingRecv = node.pingRecv
				clusterNode.info.linkStatus = node.linkStatus
			} else {
				clusterNode.info = node
			}

			for i := 8; i < len(parts); i++ {
				slots := parts[i]
				if strings.Contains(slots, "<") {
					slotStr := strings.Split(slots, "-<-")
					slotId, _ := strconv.Atoi(slotStr[0][1:])
					clusterNode.info.importing[slotId] = slotStr[1][0 : len(slotStr[1])-1]
				} else if strings.Contains(slots, ">") {
					slotStr := strings.Split(slots, "->-")
					slotId, _ := strconv.Atoi(slotStr[0][1:])
					clusterNode.info.migrating[slotId] = slotStr[1][0 : len(slotStr[1])-1]
				} else if strings.Contains(slots, "-") {
					slotStr := strings.Split(slots, "-")
					firstId, _ := strconv.Atoi(slotStr[0])
					lastId, _ := strconv.Atoi(slotStr[1])
					clusterNode.AddSlots(firstId, lastId)
				} else {
					firstId, _ := strconv.Atoi(slots)
					clusterNode.AddSlots(firstId, firstId)
				}
			}
		} else if getfriends {
			clusterNode.friends = append(clusterNode.friends, node)
		}
	}
	return nil
}

func (clusterNode *ClusterNode) AddSlots(start, end int) {
	for i := start; i <= end; i++ {
		clusterNode.info.slots = append(clusterNode.info.slots, i)
	}
	clusterNode.dirty = true
}

func (clusterNode *ClusterNode) FlushNodeConfig() {
	if !clusterNode.dirty {
		return
	}

	if clusterNode.Replicate() != "" {
		// run replicate cmd
		if _, err := clusterNode.ClusterReplicateWithNodeID(clusterNode.Replicate()); err != nil {
			// If the cluster did not already joined it is possible that
			// the slave does not know the master node yet. So on errors
			// we return ASAP leaving the dirty flag set, to flush the
			// config later.
			return
		}
	} else {
		// TODO: run addslots cmd
		var array []int
		for s, value := range clusterNode.Slots() {
			if value == NewHashSlot {
				array = append(array, s)
				clusterNode.info.slots[s] = AssignedHashSlot
			}
			clusterNode.ClusterAddSlots(array)
		}
	}

	clusterNode.dirty = false
}

// XXX: check the error for call CLUSTER addslots
func (clusterNode *ClusterNode) ClusterAddSlots(args ...interface{}) (ret string, err error) {
	return clusterNode.Call("CLUSTER", "addslots", args).Text()
}

// XXX: check the error for call CLUSTER delslots
func (clusterNode *ClusterNode) ClusterDelSlots(args ...interface{}) (ret string, err error) {
	return clusterNode.Call("CLUSTER", "delslots", args).Text()
}

func (clusterNode *ClusterNode) ClusterBumpepoch() (ret string, err error) {
	return clusterNode.Call("CLUSTER", "bumpepoch").Text()
}

func (clusterNode *ClusterNode) InfoString() (result string) {
	var role = "M"

	if !clusterNode.HasFlag("master") {
		role = "S"
	}

	keys := make([]int, 0, len(clusterNode.Slots()))

	for k := range clusterNode.Slots() {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	slotstr := MergeNumArray2NumRange(keys)

	if clusterNode.Replicate() != "" && clusterNode.dirty {
		result = fmt.Sprintf("S: %s %s", clusterNode.info.name, clusterNode.String())
	} else {
		// fix myself flag not the first element of []slots
		result = fmt.Sprintf("%s: %s %s\n\t   slots:%s (%d slots) %s",
			role, clusterNode.info.name, clusterNode.String(), slotstr, len(clusterNode.Slots()), strings.Join(clusterNode.info.flags[1:], ","))
	}

	if clusterNode.Replicate() != "" {
		result = result + fmt.Sprintf("\n\t   replicates %s", clusterNode.Replicate())
	} else {
		result = result + fmt.Sprintf("\n\t   %d additional replica(s)", len(clusterNode.replicasNodes))
	}

	return result
}

func (clusterNode *ClusterNode) GetConfigSignature() string {
	config := []string{}

	result, err := clusterNode.Call("CLUSTER", "NODES").Text()
	if err != nil {
		return ""
	}

	nodes := strings.Split(result, "\n")
	for _, val := range nodes {
		parts := strings.Split(val, " ")
		if len(parts) <= 3 {
			continue
		}

		sig := parts[0] + ":"

		slots := []string{}
		if len(parts) > 7 {
			for i := 8; i < len(parts); i++ {
				p := parts[i]
				if !strings.Contains(p, "[") {
					slots = append(slots, p)
				}
			}
		}
		if len(slots) == 0 {
			continue
		}
		sort.Strings(slots)
		sig = sig + strings.Join(slots, ",")

		config = append(config, sig)
	}

	sort.Strings(config)
	return strings.Join(config, "|")
}

///////////////////////////////////////////////////////////
// some useful struct contains cluster node.
type ClusterArray []ClusterNode

func (c ClusterArray) Len() int {
	return len(c)
}

func (c ClusterArray) Swap(i, j int) {
	c[i], c[j] = c[j], c[i]
}

func (c ClusterArray) Less(i, j int) bool {
	return len(c[i].Slots()) < len(c[j].Slots())
}

type MovedNode struct {
	Source ClusterNode
	Slot   int
}

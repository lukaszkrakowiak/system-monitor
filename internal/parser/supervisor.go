package parser

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// SupervisorProcess to jeden element z supervisor.getAllProcessInfo.
type SupervisorProcess struct {
	Name      string
	Group     string
	StateName string
	PID       int
}

// SupervisorWorker to pojedyncza instancja w grupie.
type SupervisorWorker struct {
	Name  string `json:"name"`
	State string `json:"state"`
	PID   int    `json:"pid"`
}

// SupervisorGroup to zagregowana grupa procesów.
type SupervisorGroup struct {
	Group   string             `json:"group"`
	Running int                `json:"running"`
	Total   int                `json:"total"`
	Status  string             `json:"status"` // OK | DEGRADED | DOWN
	Workers []SupervisorWorker `json:"workers"`
}

type rpcInner struct {
	Chardata string `xml:",chardata"`
	String   string `xml:"string"`
	Int      string `xml:"int"`
	I4       string `xml:"i4"`
}

func (v rpcInner) text() string {
	for _, s := range []string{v.String, v.Int, v.I4} {
		if s != "" {
			return s
		}
	}
	return strings.TrimSpace(v.Chardata)
}

type rpcMember struct {
	Name  string   `xml:"name"`
	Value rpcInner `xml:"value"`
}

type rpcStruct struct {
	Members []rpcMember `xml:"struct>member"`
}

func (s rpcStruct) fields() map[string]string {
	m := make(map[string]string, len(s.Members))
	for _, mem := range s.Members {
		m[mem.Name] = mem.Value.text()
	}
	return m
}

type rpcResponse struct {
	XMLName xml.Name    `xml:"methodResponse"`
	Items   []rpcStruct `xml:"params>param>value>array>data>value"`
	Fault   *struct {
		Value rpcStruct `xml:"value"`
	} `xml:"fault"`
}

// ParseSupervisorProcessInfo parsuje odpowiedź XML-RPC supervisor.getAllProcessInfo.
func ParseSupervisorProcessInfo(data []byte) ([]SupervisorProcess, error) {
	var resp rpcResponse
	if err := xml.NewDecoder(bytes.NewReader(data)).Decode(&resp); err != nil {
		return nil, fmt.Errorf("parser: supervisor xml-rpc: %w", err)
	}
	if resp.Fault != nil {
		f := resp.Fault.Value.fields()
		return nil, fmt.Errorf("parser: supervisor fault %s: %s", f["faultCode"], f["faultString"])
	}
	procs := make([]SupervisorProcess, 0, len(resp.Items))
	for _, it := range resp.Items {
		f := it.fields()
		if f["name"] == "" {
			return nil, fmt.Errorf("parser: supervisor process without name")
		}
		pid, _ := strconv.Atoi(f["pid"])
		procs = append(procs, SupervisorProcess{Name: f["name"], Group: f["group"], StateName: f["statename"], PID: pid})
	}
	return procs, nil
}

var trailingIndex = regexp.MustCompile(`_\d+$`)

// SupervisorGroupName stosuje regułę grupowania: "grupa:proces" -> "grupa";
// inaczej gdy supervisor podał inną grupę niż nazwa -> ta grupa;
// inaczej nazwa bez sufiksu _\d+$.
func SupervisorGroupName(name, group string) string {
	if i := strings.IndexByte(name, ':'); i > 0 {
		return name[:i]
	}
	if group != "" && group != name {
		return group
	}
	if t := trailingIndex.ReplaceAllString(name, ""); t != "" {
		return t
	}
	return name
}

// GroupSupervisor agreguje procesy w grupy (posortowane po nazwie).
// running == total && total > 0 -> OK; 0 < running < total -> DEGRADED; running == 0 -> DOWN.
func GroupSupervisor(procs []SupervisorProcess) []SupervisorGroup {
	idx := map[string]*SupervisorGroup{}
	for _, p := range procs {
		g := SupervisorGroupName(p.Name, p.Group)
		sg, ok := idx[g]
		if !ok {
			sg = &SupervisorGroup{Group: g, Workers: []SupervisorWorker{}}
			idx[g] = sg
		}
		sg.Total++
		if p.StateName == "RUNNING" {
			sg.Running++
		}
		sg.Workers = append(sg.Workers, SupervisorWorker{Name: p.Name, State: p.StateName, PID: p.PID})
	}
	out := make([]SupervisorGroup, 0, len(idx))
	for _, g := range idx {
		switch {
		case g.Running == g.Total && g.Total > 0:
			g.Status = "OK"
		case g.Running > 0:
			g.Status = "DEGRADED"
		default:
			g.Status = "DOWN"
		}
		sort.Slice(g.Workers, func(i, j int) bool { return g.Workers[i].Name < g.Workers[j].Name })
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Group < out[j].Group })
	return out
}

// SupervisorCacheStatus zamienia status grupy na OK | WARN | CRIT (kolumna health_checks_cache).
func SupervisorCacheStatus(groupStatus string) string {
	switch groupStatus {
	case "OK":
		return "OK"
	case "DEGRADED":
		return "WARN"
	}
	return "CRIT"
}

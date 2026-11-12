//go:build linux

package pathpin

import (
	"bytes"
	"log"
	"sync"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
)

const (
	// egressTableName is the mark table installed by an earlier build.
	// WARP does not exempt that mark in its filter, so the table is removed.
	egressTableName = "genesis"
	warpTableName   = "cloudflare-warp"
)

var (
	egressMu         sync.Mutex
	markTableCleared bool
)

// EnsureDefaultEgress lets packets already routed out iface pass WARP's
// filter. That filter's input and output chains drop anything that is not
// the tunnel interface (the fwmark only skips WARP's routing rule). An accept
// here ends WARP's chain; later firewalld chains still see the packet.
// Missing WARP table is success. The caller retries while WARP is up.
func EnsureDefaultEgress(iface string) error {
	if _, err := oifNameData(iface); err != nil {
		return err
	}
	egressMu.Lock()
	defer egressMu.Unlock()
	if err := deleteMarkTable(); err != nil {
		return err
	}
	table, err := warpTable()
	if err != nil || table == nil {
		return err
	}
	for _, chainName := range []string{"input", "output"} {
		if err := ensureWarpAllow(table, chainName, iface); err != nil {
			return err
		}
	}
	return nil
}

// ClearDefaultEgress removes the obsolete mark table and the accepts added to
// WARP's filter. Safe when WARP is not running.
func ClearDefaultEgress() error {
	egressMu.Lock()
	defer egressMu.Unlock()
	if err := deleteMarkTable(); err != nil {
		return err
	}
	table, err := warpTable()
	if err != nil || table == nil {
		return err
	}
	for _, chainName := range []string{"input", "output"} {
		if err := deleteWarpAllows(table, chainName); err != nil {
			return err
		}
	}
	return nil
}

func warpTable() (*nftables.Table, error) {
	c, err := nftables.New()
	if err != nil {
		return nil, err
	}
	tables, err := c.ListTables()
	if err != nil {
		return nil, err
	}
	for _, t := range tables {
		if t.Name == warpTableName && t.Family == nftables.TableFamilyINet {
			return t, nil
		}
	}
	return nil, nil
}

func ensureWarpAllow(table *nftables.Table, chainName, iface string) error {
	c, err := nftables.New()
	if err != nil {
		return err
	}
	chain, err := c.ListChain(table, chainName)
	if err != nil {
		return err
	}
	rules, err := c.GetRules(table, chain)
	if err != nil {
		return err
	}
	want := warpAllowUserData(iface)
	prefix := []byte(warpAllowPrefix)
	keep := false
	for _, rule := range rules {
		if bytes.Equal(rule.UserData, want) {
			keep = true
			continue
		}
		if bytes.HasPrefix(rule.UserData, prefix) {
			if err := c.DelRule(rule); err != nil {
				return err
			}
		}
	}
	if !keep {
		c.InsertRule(allowRule(table, chain, iface, want))
		log.Printf("pathpin: accept %s on WARP %s filter", iface, chainName)
	}
	return c.Flush()
}

func deleteWarpAllows(table *nftables.Table, chainName string) error {
	c, err := nftables.New()
	if err != nil {
		return err
	}
	chain, err := c.ListChain(table, chainName)
	if err != nil {
		if isMissing(err) {
			return nil
		}
		return err
	}
	rules, err := c.GetRules(table, chain)
	if err != nil {
		return err
	}
	prefix := []byte(warpAllowPrefix)
	for _, rule := range rules {
		if bytes.HasPrefix(rule.UserData, prefix) {
			if err := c.DelRule(rule); err != nil {
				return err
			}
		}
	}
	return c.Flush()
}

func allowRule(table *nftables.Table, chain *nftables.Chain, iface string, userData []byte) *nftables.Rule {
	key := expr.MetaKeyOIFNAME
	if chain.Name == "input" {
		key = expr.MetaKeyIIFNAME
	}
	name, _ := oifNameData(iface)
	return &nftables.Rule{
		Table:    table,
		Chain:    chain,
		UserData: userData,
		Exprs: []expr.Any{
			&expr.Meta{Key: key, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: name},
			&expr.Verdict{Kind: expr.VerdictAccept},
		},
	}
}

func deleteMarkTable() error {
	if markTableCleared {
		return nil
	}
	c, err := nftables.New()
	if err != nil {
		return err
	}
	c.DelTable(&nftables.Table{
		Family: nftables.TableFamilyINet,
		Name:   egressTableName,
	})
	if err := c.Flush(); err != nil && !isMissing(err) {
		return err
	}
	markTableCleared = true
	return nil
}

func isMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return bytes.Contains([]byte(msg), []byte("no such file")) ||
		bytes.Contains([]byte(msg), []byte("ENOENT")) ||
		bytes.Contains([]byte(msg), []byte("not found")) ||
		bytes.Contains([]byte(msg), []byte("does not exist"))
}

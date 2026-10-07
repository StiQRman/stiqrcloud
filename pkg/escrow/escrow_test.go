package escrow

import (
	"testing"
)

func TestEscrowPricingAndContractLifecycle(t *testing.T) {
	mgr := NewManager("node_auditor_1", 100)
	_ = mgr.SetFreeForAll(false)

	rates := map[string]int64{
		"storage_1": 150,
		"storage_2": 100,
		"storage_3": 200,
	}
	auditors := []string{"node_auditor_1", "node_auditor_2", "node_auditor_3"}

	contract, err := mgr.CreateContract("doc_xyz", "owner_pub_123", 10*1024*1024, 3, rates, auditors)
	if err != nil {
		t.Fatalf("CreateContract failed: %v", err)
	}

	if contract.TotalSats <= 0 {
		t.Fatalf("Expected positive total sats")
	}
	if contract.IsSettled {
		t.Fatalf("Contract should not be settled yet")
	}
	if contract.IsFree {
		t.Fatalf("Contract should not be free by default")
	}

	// 1. Settle
	settled, err := mgr.SettlePayment(contract.ContractID)
	if err != nil || !settled.IsSettled {
		t.Fatalf("SettlePayment failed: %v", err)
	}

	// 2. Audit pass releases micropayment
	disbursed, err := mgr.RecordAuditPass(contract.ContractID, "storage_1")
	if err != nil || disbursed <= 0 {
		t.Fatalf("RecordAuditPass failed to disburse sats: %v", err)
	}

	// 3. Reallocate on node failure
	err = mgr.ReallocateOfflineNode(contract.ContractID, "storage_2", "storage_4_replacement")
	if err != nil {
		t.Fatalf("ReallocateOfflineNode failed: %v", err)
	}

	c, _ := mgr.GetContract(contract.ContractID)
	if _, ok := c.StorageNodes["storage_4_replacement"]; !ok {
		t.Fatalf("Replacement node not registered in contract")
	}
}

func TestEscrowWhitelistAndFreeForAll(t *testing.T) {
	mgr := NewManager("node_auditor_1", 100)
	_ = mgr.SetFreeForAll(false)

	// Add VIP address to whitelist
	vipAddr := "ed25519_vip_address_xyz"
	if err := mgr.AddWhitelist(vipAddr); err != nil {
		t.Fatalf("AddWhitelist failed: %v", err)
	}

	if !mgr.IsWhitelisted(vipAddr) {
		t.Fatalf("Expected address to be whitelisted")
	}

	// Contract for whitelisted address must be free and auto-settled
	auditors := []string{"aud1", "aud2", "aud3"}
	rates := map[string]int64{"s1": 100, "s2": 100, "s3": 100}
	cFree, err := mgr.CreateContract("doc_vip", vipAddr, 1024*1024, 1, rates, auditors)
	if err != nil {
		t.Fatalf("CreateContract failed: %v", err)
	}
	if !cFree.IsFree || cFree.TotalSats != 0 || !cFree.IsSettled {
		t.Fatalf("Expected VIP contract to be free (0 sats) and auto-settled")
	}

	// Non-whitelisted client still gets charged
	cPaid, err := mgr.CreateContract("doc_regular", "regular_client_abc", 1024*1024, 1, rates, auditors)
	if err != nil {
		t.Fatalf("CreateContract failed: %v", err)
	}
	if cPaid.IsFree || cPaid.TotalSats <= 0 || cPaid.IsSettled {
		t.Fatalf("Expected regular contract to require payment")
	}

	// Now toggle FreeForAll = true
	if err := mgr.SetFreeForAll(true); err != nil {
		t.Fatalf("SetFreeForAll failed: %v", err)
	}

	// Now even regular client gets free storage
	cFreeAll, err := mgr.CreateContract("doc_free_all", "regular_client_abc", 1024*1024, 1, rates, auditors)
	if err != nil {
		t.Fatalf("CreateContract failed: %v", err)
	}
	if !cFreeAll.IsFree || cFreeAll.TotalSats != 0 || !cFreeAll.IsSettled {
		t.Fatalf("Expected FreeForAll to grant free storage to regular client")
	}
}

package escrow

import (
	"testing"
)

func TestEscrowPricingAndContractLifecycle(t *testing.T) {
	mgr := NewManager("node_auditor_1", 100)

	rates := map[string]int64{
		"storage_1": 150,
		"storage_2": 100,
		"storage_3": 200,
	}
	auditors := []string{"node_auditor_1", "node_auditor_2", "node_auditor_3"}

	contract, err := mgr.CreateContract("doc_xyz", 10*1024*1024, 3, rates, auditors)
	if err != nil {
		t.Fatalf("CreateContract failed: %v", err)
	}

	if contract.TotalSats <= 0 {
		t.Fatalf("Expected positive total sats")
	}
	if contract.IsSettled {
		t.Fatalf("Contract should not be settled yet")
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

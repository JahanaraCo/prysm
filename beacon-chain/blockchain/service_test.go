package blockchain

import (
	"context"
	"io/ioutil"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	gethTypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
	"github.com/prysmaticlabs/prysm/beacon-chain/params"
	"github.com/prysmaticlabs/prysm/beacon-chain/powchain"
	"github.com/prysmaticlabs/prysm/beacon-chain/types"
	pb "github.com/prysmaticlabs/prysm/proto/beacon/p2p/v1"
	"github.com/prysmaticlabs/prysm/shared/database"
	"github.com/prysmaticlabs/prysm/shared/testutil"
	"github.com/sirupsen/logrus"
	logTest "github.com/sirupsen/logrus/hooks/test"
)

func init() {
	logrus.SetLevel(logrus.DebugLevel)
	logrus.SetOutput(ioutil.Discard)
}

type mockClient struct{}

func (f *mockClient) SubscribeNewHead(ctx context.Context, ch chan<- *gethTypes.Header) (ethereum.Subscription, error) {
	return new(event.Feed).Subscribe(ch), nil
}

func (f *mockClient) BlockByHash(ctx context.Context, hash common.Hash) (*gethTypes.Block, error) {
	return nil, nil
}

func (f *mockClient) SubscribeFilterLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- gethTypes.Log) (ethereum.Subscription, error) {
	return new(event.Feed).Subscribe(ch), nil
}

func (f *mockClient) LatestBlockHash() common.Hash {
	return common.BytesToHash([]byte{'A'})
}

func TestStartStop(t *testing.T) {
	ctx := context.Background()

	config := &database.DBConfig{DataDir: "", Name: "", InMemory: true}
	db, err := database.NewDB(config)
	if err != nil {
		t.Fatalf("could not setup beaconDB: %v", err)
	}

	endpoint := "ws://127.0.0.1"
	client := &mockClient{}
	web3Service, err := powchain.NewWeb3Service(ctx, &powchain.Web3ServiceConfig{Endpoint: endpoint, Pubkey: "", VrcAddr: common.Address{}}, client, client, client)
	if err != nil {
		t.Fatalf("unable to set up web3 service: %v", err)
	}
	beaconChain, err := NewBeaconChain(db.DB())
	cfg := &Config{
		BeaconBlockBuf: 0,
		BeaconDB:       db.DB(),
		Chain:          beaconChain,
	}
	if err != nil {
		t.Fatalf("could not register blockchain service: %v", err)
	}
	chainService, err := NewChainService(ctx, cfg)
	if err != nil {
		t.Fatalf("unable to setup chain service: %v", err)
	}
	chainService.Start()

	cfg = &Config{
		BeaconBlockBuf: 0,
		BeaconDB:       db.DB(),
		Chain:          beaconChain,
		Web3Service:    web3Service,
	}
	chainService, err = NewChainService(ctx, cfg)
	if err != nil {
		t.Fatalf("unable to setup chain service: %v", err)
	}
	chainService.Start()

	if len(chainService.CurrentActiveState().RecentBlockHashes()) != 128 {
		t.Errorf("incorrect recent block hashes")
	}

	if len(chainService.CurrentCrystallizedState().Validators()) != params.BootstrappedValidatorsCount {
		t.Errorf("incorrect default validator size")
	}
	if chainService.ContainsBlock([32]byte{}) {
		t.Errorf("chain is not empty")
	}
	hasState, err := chainService.HasStoredState()
	if err != nil {
		t.Fatalf("calling HasStoredState failed")
	}
	if hasState {
		t.Errorf("has stored state should return false")
	}
	chainService.CanonicalBlockFeed()
	chainService.CanonicalCrystallizedStateFeed()

	chainService, _ = NewChainService(ctx, cfg)

	active := types.NewActiveState(&pb.ActiveState{RecentBlockHashes: [][]byte{{'A'}}}, make(map[[32]byte]*types.VoteCache))

	activeStateHash, err := active.Hash()
	if err != nil {
		t.Fatalf("Cannot hash active state: %v", err)
	}
	chainService.chain.SetActiveState(active)

	crystallized := types.NewCrystallizedState(&pb.CrystallizedState{LastStateRecalc: 10000})
	crystallizedStateHash, err := crystallized.Hash()
	if err != nil {
		t.Fatalf("Cannot hash crystallized state: %v", err)
	}
	chainService.chain.SetCrystallizedState(crystallized)

	parentBlock := NewBlock(t, nil)
	parentHash, _ := parentBlock.Hash()

	block := NewBlock(t, &pb.BeaconBlock{
		SlotNumber:            2,
		ActiveStateHash:       activeStateHash[:],
		CrystallizedStateHash: crystallizedStateHash[:],
		ParentHash:            parentHash[:],
		PowChainRef:           []byte("a"),
	})
	if err := chainService.SaveBlock(block); err != nil {
		t.Errorf("save block should have failed")
	}

	// Save states so HasStoredState state should return true.
	chainService.chain.SetActiveState(types.NewActiveState(&pb.ActiveState{}, make(map[[32]byte]*types.VoteCache)))
	chainService.chain.SetCrystallizedState(types.NewCrystallizedState(&pb.CrystallizedState{}))
	hasState, _ = chainService.HasStoredState()
	if !hasState {
		t.Errorf("has stored state should return false")
	}

	if err := chainService.Stop(); err != nil {
		t.Fatalf("unable to stop chain service: %v", err)
	}

	// The context should have been canceled.
	if chainService.ctx.Err() == nil {
		t.Error("context was not canceled")
	}
}

func TestFaultyStop(t *testing.T) {
	ctx := context.Background()
	config := &database.DBConfig{DataDir: "", Name: "", InMemory: true}
	db, err := database.NewDB(config)
	if err != nil {
		t.Fatalf("could not setup beaconDB: %v", err)

	}
	endpoint := "ws://127.0.0.1"
	client := &mockClient{}
	web3Service, err := powchain.NewWeb3Service(ctx, &powchain.Web3ServiceConfig{Endpoint: endpoint, Pubkey: "", VrcAddr: common.Address{}}, client, client, client)
	if err != nil {
		t.Fatalf("unable to set up web3 service: %v", err)
	}
	beaconChain, err := NewBeaconChain(db.DB())
	if err != nil {
		t.Fatalf("could not register blockchain service: %v", err)
	}
	cfg := &Config{
		BeaconBlockBuf: 0,
		BeaconDB:       db.DB(),
		Chain:          beaconChain,
		Web3Service:    web3Service,
	}

	chainService, err := NewChainService(ctx, cfg)
	if err != nil {
		t.Fatalf("unable to setup chain service: %v", err)
	}

	chainService.Start()

	chainService.chain.SetActiveState(types.NewActiveState(nil, make(map[[32]byte]*types.VoteCache)))

	err = chainService.Stop()
	if err == nil {
		t.Errorf("chain stop should have failed with persist active state")
	}

	chainService.chain.SetActiveState(types.NewActiveState(&pb.ActiveState{}, make(map[[32]byte]*types.VoteCache)))

	chainService.chain.SetCrystallizedState(types.NewCrystallizedState(nil))
	err = chainService.Stop()
	if err == nil {
		t.Errorf("chain stop should have failed with persist crystallized state")
	}
}

func TestRunningChainService(t *testing.T) {
	hook := logTest.NewGlobal()
	ctx := context.Background()
	config := &database.DBConfig{DataDir: "", Name: "", InMemory: true}
	db, err := database.NewDB(config)
	if err != nil {
		t.Fatalf("could not setup beaconDB: %v", err)

	}
	endpoint := "ws://127.0.0.1"
	client := &mockClient{}
	web3Service, err := powchain.NewWeb3Service(ctx, &powchain.Web3ServiceConfig{Endpoint: endpoint, Pubkey: "", VrcAddr: common.Address{}}, client, client, client)
	if err != nil {
		t.Fatalf("unable to set up web3 service: %v", err)
	}
	beaconChain, err := NewBeaconChain(db.DB())
	if err != nil {
		t.Fatalf("could not register blockchain service: %v", err)
	}

	active, crystallized, err := types.NewGenesisStates()
	if err != nil {
		t.Fatalf("Can't generate genesis state: %v", err)
	}

	activeStateHash, _ := active.Hash()
	crystallizedStateHash, _ := crystallized.Hash()

	cfg := &Config{
		BeaconBlockBuf: 0,
		BeaconDB:       db.DB(),
		Chain:          beaconChain,
		Web3Service:    web3Service,
	}
	chainService, _ := NewChainService(ctx, cfg)

	genesis, err := beaconChain.GenesisBlock()
	if err != nil {
		t.Fatalf("unable to get canonical head: %v", err)
	}

	parentHash, err := genesis.Hash()
	if err != nil {
		t.Fatalf("unable to get hash of canonical head: %v", err)
	}

	block := NewBlock(t, &pb.BeaconBlock{
		SlotNumber:            1,
		ActiveStateHash:       activeStateHash[:],
		CrystallizedStateHash: crystallizedStateHash[:],
		ParentHash:            parentHash[:],
		PowChainRef:           []byte("a"),
		Attestations: []*pb.AttestationRecord{{
			Slot:             0,
			AttesterBitfield: []byte{'A', 'B'},
			ShardId:          0,
		}},
	})

	exitRoutine := make(chan bool)
	go func() {
		chainService.blockProcessing(chainService.ctx.Done())
		<-exitRoutine
	}()
	if err := chainService.SaveBlock(block); err != nil {
		t.Fatal(err)
	}

	chainService.incomingBlockChan <- block
	chainService.cancel()
	exitRoutine <- true

	testutil.AssertLogsContain(t, hook, "Finished processing state for candidate block")
}

func TestUpdateHead(t *testing.T) {
	hook := logTest.NewGlobal()
	ctx := context.Background()
	config := &database.DBConfig{DataDir: "", Name: "", InMemory: true}
	db, err := database.NewDB(config)
	if err != nil {
		t.Fatalf("could not setup beaconDB: %v", err)

	}
	endpoint := "ws://127.0.0.1"
	client := &mockClient{}
	web3Service, err := powchain.NewWeb3Service(ctx, &powchain.Web3ServiceConfig{Endpoint: endpoint, Pubkey: "", VrcAddr: common.Address{}}, client, client, client)
	if err != nil {
		t.Fatalf("unable to set up web3 service: %v", err)
	}
	beaconChain, err := NewBeaconChain(db.DB())
	if err != nil {
		t.Fatalf("could not register blockchain service: %v", err)
	}
	cfg := &Config{
		BeaconBlockBuf:   0,
		IncomingBlockBuf: 0,
		BeaconDB:         db.DB(),
		Chain:            beaconChain,
		Web3Service:      web3Service,
	}
	chainService, _ := NewChainService(ctx, cfg)

	active, crystallized, err := types.NewGenesisStates()
	if err != nil {
		t.Fatalf("Can't generate genesis state: %v", err)
	}
	activeStateHash, _ := active.Hash()
	crystallizedStateHash, _ := crystallized.Hash()

	parentHash := [32]byte{'a'}

	block := NewBlock(t, &pb.BeaconBlock{
		SlotNumber:            64,
		ActiveStateHash:       activeStateHash[:],
		CrystallizedStateHash: crystallizedStateHash[:],
		ParentHash:            parentHash[:],
		PowChainRef:           []byte("a"),
	})

	chainService.candidateBlock = block
	chainService.candidateActiveState = active
	chainService.candidateCrystallizedState = crystallized

	chainService.updateHead(64)
	testutil.AssertLogsContain(t, hook, "Canonical block determined")

	if chainService.candidateBlock != nilBlock {
		t.Error("Candidate Block unable to be reset")
	}
}

func TestProcessingBlockWithAttestations(t *testing.T) {
	ctx := context.Background()
	config := &database.DBConfig{DataDir: "", Name: "", InMemory: true}
	db, err := database.NewDB(config)
	if err != nil {
		t.Fatalf("could not setup beaconDB: %v", err)

	}
	endpoint := "ws://127.0.0.1"
	client := &mockClient{}
	web3Service, err := powchain.NewWeb3Service(ctx, &powchain.Web3ServiceConfig{Endpoint: endpoint, Pubkey: "", VrcAddr: common.Address{}}, client, client, client)
	if err != nil {
		t.Fatalf("unable to set up web3 service: %v", err)
	}
	beaconChain, err := NewBeaconChain(db.DB())
	if err != nil {
		t.Fatalf("could not register blockchain service: %v", err)
	}

	var validators []*pb.ValidatorRecord
	for i := 0; i < 40; i++ {
		validator := &pb.ValidatorRecord{Balance: 32, StartDynasty: 1, EndDynasty: 10}
		validators = append(validators, validator)
	}

	crystallized := types.NewCrystallizedState(&pb.CrystallizedState{
		LastStateRecalc: 0,
		Validators:      validators,
		CurrentDynasty:  5,
		ShardAndCommitteesForSlots: []*pb.ShardAndCommitteeArray{
			{
				ArrayShardAndCommittee: []*pb.ShardAndCommittee{
					{ShardId: 0, Committee: []uint32{0, 1, 2, 3, 4, 5}},
				},
			},
		},
	})
	crystallizedStateHash, err := crystallized.Hash()
	if err != nil {
		t.Fatalf("Cannot hash crystallized state: %v", err)
	}
	if err := beaconChain.SetCrystallizedState(crystallized); err != nil {
		t.Fatalf("unable to mutate crystallized state: %v", err)
	}

	var recentBlockHashes [][]byte
	for i := 0; i < params.CycleLength+1; i++ {
		recentBlockHashes = append(recentBlockHashes, []byte{'X'})
	}
	active := types.NewActiveState(&pb.ActiveState{RecentBlockHashes: recentBlockHashes}, make(map[[32]byte]*types.VoteCache))

	if err := beaconChain.SetActiveState(active); err != nil {
		t.Fatalf("unable to mutate active state: %v", err)
	}
	cfg := &Config{
		BeaconBlockBuf: 0,
		BeaconDB:       db.DB(),
		Chain:          beaconChain,
		Web3Service:    web3Service,
	}

	chainService, _ := NewChainService(ctx, cfg)

	exitRoutine := make(chan bool)
	go func() {
		chainService.blockProcessing(chainService.ctx.Done())
		<-exitRoutine
	}()

	parentBlock := NewBlock(t, nil)
	if err := chainService.SaveBlock(parentBlock); err != nil {
		t.Fatal(err)
	}
	parentHash, _ := parentBlock.Hash()

	activeStateHash, err := active.Hash()
	if err != nil {
		t.Fatalf("Cannot hash active state: %v", err)
	}

	block := NewBlock(t, &pb.BeaconBlock{
		SlotNumber:            2,
		ActiveStateHash:       activeStateHash[:],
		CrystallizedStateHash: crystallizedStateHash[:],
		ParentHash:            parentHash[:],
		PowChainRef:           []byte("a"),
		Attestations: []*pb.AttestationRecord{
			{Slot: 0, ShardId: 0, AttesterBitfield: []byte{'0'}},
		},
	})

	chainService.incomingBlockChan <- block
	chainService.cancel()
	exitRoutine <- true

}

func TestProcessingBlocks(t *testing.T) {
	ctx := context.Background()
	config := &database.DBConfig{DataDir: "", Name: "", InMemory: true}
	db, err := database.NewDB(config)
	if err != nil {
		t.Fatalf("could not setup beaconDB: %v", err)
	}

	endpoint := "ws://127.0.0.1"
	client := &mockClient{}
	web3Service, err := powchain.NewWeb3Service(ctx, &powchain.Web3ServiceConfig{Endpoint: endpoint, Pubkey: "", VrcAddr: common.Address{}}, client, client, client)
	if err != nil {
		t.Fatalf("unable to set up web3 service: %v", err)
	}
	beaconChain, err := NewBeaconChain(db.DB())
	if err != nil {
		t.Fatalf("could not register blockchain service: %v", err)
	}

	cfg := &Config{
		BeaconBlockBuf: 0,
		BeaconDB:       db.DB(),
		Chain:          beaconChain,
		Web3Service:    web3Service,
	}

	chainService, _ := NewChainService(ctx, cfg)

	active, crystallized, err := types.NewGenesisStates()
	if err != nil {
		t.Fatalf("Can't generate genesis state: %v", err)
	}

	activeStateHash, _ := active.Hash()
	crystallizedStateHash, _ := crystallized.Hash()

	genesis, err := beaconChain.GenesisBlock()
	if err != nil {
		t.Fatalf("unable to get canonical head: %v", err)
	}

	parentHash, err := genesis.Hash()
	if err != nil {
		t.Fatalf("unable to get hash of canonical head: %v", err)
	}

	block1 := NewBlock(t, &pb.BeaconBlock{
		ParentHash:            parentHash[:],
		SlotNumber:            1,
		ActiveStateHash:       activeStateHash[:],
		CrystallizedStateHash: crystallizedStateHash[:],
		Attestations: []*pb.AttestationRecord{{
			Slot:             0,
			AttesterBitfield: []byte{0, 0},
			ShardId:          0,
		}},
	})

	exitRoutine := make(chan bool)
	go func() {
		chainService.blockProcessing(chainService.ctx.Done())
		<-exitRoutine
	}()

	if err := chainService.SaveBlock(block1); err != nil {
		t.Fatal(err)
	}

	chainService.incomingBlockChan <- block1

	block1Hash, err := block1.Hash()
	if err != nil {
		t.Fatalf("unable to get hash of block 1: %v", err)
	}

	// Add 1 more attestation field for slot2
	block2 := NewBlock(t, &pb.BeaconBlock{
		ParentHash: block1Hash[:],
		SlotNumber: 2,
		Attestations: []*pb.AttestationRecord{
			{Slot: 0, AttesterBitfield: []byte{0, 0}, ShardId: 0},
			{Slot: 1, AttesterBitfield: []byte{0, 0}, ShardId: 0},
		}})

	chainService.incomingBlockChan <- block2

	block2Hash, err := block2.Hash()
	if err != nil {
		t.Fatalf("unable to get hash of block 1: %v", err)
	}

	// Add 1 more attestation field for slot3
	block3 := NewBlock(t, &pb.BeaconBlock{
		ParentHash: block2Hash[:],
		SlotNumber: 3,
		Attestations: []*pb.AttestationRecord{
			{Slot: 0, AttesterBitfield: []byte{0, 0}, ShardId: 0},
			{Slot: 1, AttesterBitfield: []byte{0, 0}, ShardId: 0},
			{Slot: 2, AttesterBitfield: []byte{0, 0}, ShardId: 0},
		}})

	chainService.incomingBlockChan <- block3

	chainService.cancel()
	exitRoutine <- true

	// We should have 6 pending attestations from block 1 to block 3
	if len(beaconChain.ActiveState().PendingAttestations()) != 6 {
		t.Fatalf("Active state should have 6 pending attestation: %d", len(beaconChain.ActiveState().PendingAttestations()))
	}
}

func TestProcessAttestationBadBlock(t *testing.T) {
	hook := logTest.NewGlobal()
	ctx := context.Background()
	config := &database.DBConfig{DataDir: "", Name: "", InMemory: true}
	db, err := database.NewDB(config)
	if err != nil {
		t.Fatalf("could not setup beaconDB: %v", err)
	}

	endpoint := "ws://127.0.0.1"
	client := &mockClient{}
	web3Service, err := powchain.NewWeb3Service(ctx, &powchain.Web3ServiceConfig{Endpoint: endpoint, Pubkey: "", VrcAddr: common.Address{}}, client, client, client)
	if err != nil {
		t.Fatalf("unable to set up web3 service: %v", err)
	}
	beaconChain, err := NewBeaconChain(db.DB())
	if err != nil {
		t.Fatalf("could not register blockchain service: %v", err)
	}

	cfg := &Config{
		BeaconBlockBuf: 0,
		BeaconDB:       db.DB(),
		Chain:          beaconChain,
		Web3Service:    web3Service,
	}

	chainService, _ := NewChainService(ctx, cfg)

	active, crystallized, err := types.NewGenesisStates()
	if err != nil {
		t.Fatalf("Can't generate genesis state: %v", err)
	}

	activeStateHash, _ := active.Hash()
	crystallizedStateHash, _ := crystallized.Hash()

	genesis, err := beaconChain.GenesisBlock()
	if err != nil {
		t.Fatalf("unable to get canonical head: %v", err)
	}

	parentHash, err := genesis.Hash()
	if err != nil {
		t.Fatalf("unable to get hash of canonical head: %v", err)
	}

	block1 := NewBlock(t, &pb.BeaconBlock{
		ParentHash:            parentHash[:],
		SlotNumber:            1,
		ActiveStateHash:       activeStateHash[:],
		CrystallizedStateHash: crystallizedStateHash[:],
		Attestations: []*pb.AttestationRecord{{
			Slot:             10,
			AttesterBitfield: []byte{},
			ShardId:          0,
		}},
	})

	exitRoutine := make(chan bool)
	go func() {
		chainService.blockProcessing(chainService.ctx.Done())
		<-exitRoutine
	}()

	if err := chainService.SaveBlock(block1); err != nil {
		t.Fatal(err)
	}

	chainService.incomingBlockChan <- block1

	chainService.cancel()
	exitRoutine <- true

	testutil.AssertLogsContain(t, hook, "could not process attestation for block")
}

func TestEnterCycleTransition(t *testing.T) {
	hook := logTest.NewGlobal()
	ctx := context.Background()
	config := &database.DBConfig{DataDir: "", Name: "", InMemory: true}
	db, err := database.NewDB(config)
	if err != nil {
		t.Fatalf("could not setup beaconDB: %v", err)
	}

	endpoint := "ws://127.0.0.1"
	client := &mockClient{}
	web3Service, err := powchain.NewWeb3Service(ctx, &powchain.Web3ServiceConfig{Endpoint: endpoint, Pubkey: "", VrcAddr: common.Address{}}, client, client, client)
	if err != nil {
		t.Fatalf("unable to set up web3 service: %v", err)
	}
	beaconChain, err := NewBeaconChain(db.DB())
	if err != nil {
		t.Fatalf("could not register blockchain service: %v", err)
	}

	cfg := &Config{
		BeaconBlockBuf: 0,
		BeaconDB:       db.DB(),
		Chain:          beaconChain,
		Web3Service:    web3Service,
	}

	chainService, _ := NewChainService(ctx, cfg)

	active, crystallized, err := types.NewGenesisStates()
	if err != nil {
		t.Fatalf("Can't generate genesis state: %v", err)
	}

	activeStateHash, _ := active.Hash()
	crystallizedStateHash, _ := crystallized.Hash()

	genesis, err := beaconChain.GenesisBlock()
	if err != nil {
		t.Fatalf("unable to get canonical head: %v", err)
	}
	if err := chainService.SaveBlock(genesis); err != nil {
		t.Fatalf("save block should failed")
	}

	parentHash, err := genesis.Hash()
	if err != nil {
		t.Fatalf("unable to get hash of canonical head: %v", err)
	}

	block1 := NewBlock(t, &pb.BeaconBlock{
		ParentHash:            parentHash[:],
		SlotNumber:            64,
		ActiveStateHash:       activeStateHash[:],
		CrystallizedStateHash: crystallizedStateHash[:],
		Attestations: []*pb.AttestationRecord{{
			Slot:             0,
			AttesterBitfield: []byte{0, 0},
			ShardId:          0,
		}},
	})

	exitRoutine := make(chan bool)
	go func() {
		chainService.blockProcessing(chainService.ctx.Done())
		<-exitRoutine
	}()

	if err := chainService.SaveBlock(block1); err != nil {
		t.Fatal(err)
	}

	chainService.incomingBlockChan <- block1

	chainService.cancel()
	exitRoutine <- true

	testutil.AssertLogsContain(t, hook, "Entering cycle transition")
}

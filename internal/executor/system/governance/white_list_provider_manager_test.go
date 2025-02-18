package governance

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethtype "github.com/ethereum/go-ethereum/core/types"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/axiomesh/axiom-kit/types"
	"github.com/axiomesh/axiom-ledger/internal/executor/system/access"
	"github.com/axiomesh/axiom-ledger/internal/executor/system/common"
	"github.com/axiomesh/axiom-ledger/internal/ledger"
	"github.com/axiomesh/axiom-ledger/internal/ledger/mock_ledger"
	"github.com/axiomesh/axiom-ledger/pkg/repo"
	vm "github.com/axiomesh/eth-kit/evm"
)

const (
	WhiteListProvider1 = "0x1000000000000000000000000000000000000001"
	WhiteListProvider2 = "0x1000000000000000000000000000000000000002"
)

type TestWhiteListProviderProposal struct {
	ID          uint64
	Type        ProposalType
	Proposer    string
	TotalVotes  uint64
	PassVotes   []string
	RejectVotes []string
	Status      ProposalStatus
	Providers   []access.WhiteListProvider
}

func TestWhiteListProviderManager_RunForPropose(t *testing.T) {
	wlpm := NewWhiteListProviderManager(&common.SystemContractConfig{
		Logger: logrus.New(),
	})

	mockCtl := gomock.NewController(t)
	stateLedger := mock_ledger.NewMockStateLedger(mockCtl)

	account := ledger.NewMockAccount(1, types.NewAddressByStr(common.WhiteListProviderManagerContractAddr))

	stateLedger.EXPECT().GetOrCreateAccount(gomock.Any()).Return(account).AnyTimes()
	stateLedger.EXPECT().AddLog(gomock.Any()).AnyTimes()
	stateLedger.EXPECT().SetBalance(gomock.Any(), gomock.Any()).AnyTimes()

	err := InitCouncilMembers(stateLedger, []*repo.Admin{
		{
			Address: admin1,
			Weight:  1,
			Name:    "111",
		},
		{
			Address: admin2,
			Weight:  1,
			Name:    "222",
		},
		{
			Address: admin3,
			Weight:  1,
			Name:    "333",
		},
		{
			Address: admin4,
			Weight:  1,
			Name:    "444",
		},
	}, "10")
	assert.Nil(t, err)

	testcases := []struct {
		Caller   string
		Data     []byte
		Expected vm.ExecutionResult
		Err      error
	}{
		{
			Caller: WhiteListProvider1,
			Data: generateProviderProposeData(t, WhiteListProviderAdd, access.WhiteListProviderArgs{
				Providers: []access.WhiteListProvider{
					{
						WhiteListProviderAddr: WhiteListProvider1,
					},
					{
						WhiteListProviderAddr: WhiteListProvider2,
					},
				},
			}),
			Expected: vm.ExecutionResult{},
			Err:      ErrNotFoundCouncilMember,
		},
		{
			Caller: admin1,
			Data: generateProviderProposeData(t, WhiteListProviderAdd, access.WhiteListProviderArgs{
				Providers: []access.WhiteListProvider{
					{
						WhiteListProviderAddr: WhiteListProvider1,
					},
					{
						WhiteListProviderAddr: WhiteListProvider2,
					},
				},
			}),
			Expected: vm.ExecutionResult{
				UsedGas: common.CalculateDynamicGas(generateProviderProposeData(t, WhiteListProviderAdd, access.WhiteListProviderArgs{
					Providers: []access.WhiteListProvider{
						{
							WhiteListProviderAddr: WhiteListProvider1,
						},
						{
							WhiteListProviderAddr: WhiteListProvider2,
						},
					},
				})),
				ReturnData: generateManagerReturnData(t, &TestWhiteListProviderProposal{
					ID:          1,
					Type:        WhiteListProviderAdd,
					Proposer:    admin1,
					TotalVotes:  4,
					PassVotes:   []string{admin1},
					RejectVotes: nil,
					Status:      Voting,
					Providers: []access.WhiteListProvider{
						{
							WhiteListProviderAddr: WhiteListProvider1,
						},
						{
							WhiteListProviderAddr: WhiteListProvider2,
						},
					},
				}),
			},
			Err: nil,
		},
		{
			Caller: admin1,
			Data:   []byte{0, 1, 2, 3},
			Expected: vm.ExecutionResult{
				UsedGas:    common.CalculateDynamicGas([]byte{0, 1, 2, 3}),
				ReturnData: nil,
				Err:        ErrMethodName,
			},
			Err: ErrMethodName,
		},
	}

	for _, test := range testcases {
		wlpm.Reset(1, stateLedger)

		result, err := wlpm.Run(&vm.Message{
			From: types.NewAddressByStr(test.Caller).ETHAddress(),
			Data: test.Data,
		})
		assert.Equal(t, test.Err, err)

		if result != nil {
			assert.Equal(t, test.Expected.Err, result.Err)
			assert.Equal(t, test.Expected.UsedGas, result.UsedGas)

			expectedProposal := &WhiteListProviderProposal{}
			err = json.Unmarshal(test.Expected.ReturnData, expectedProposal)
			assert.Nil(t, err)

			actualProposal := &WhiteListProviderProposal{}
			err = json.Unmarshal(result.ReturnData, actualProposal)
			assert.Nil(t, err)
			assert.Equal(t, *expectedProposal, *actualProposal)

			state, _ := account.GetState([]byte(fmt.Sprintf("%s%d", WhiteListProviderProposalKey, actualProposal.ID)))
			assert.Equal(t, true, state)
		}
	}
}

func TestWhiteListProviderManager_RunForVoteAdd(t *testing.T) {
	wlpm := NewWhiteListProviderManager(&common.SystemContractConfig{
		Logger: logrus.New(),
	})

	mockCtl := gomock.NewController(t)
	stateLedger := mock_ledger.NewMockStateLedger(mockCtl)

	account := ledger.NewMockAccount(1, types.NewAddressByStr(common.WhiteListProviderManagerContractAddr))

	stateLedger.EXPECT().GetOrCreateAccount(gomock.Any()).Return(account).AnyTimes()
	stateLedger.EXPECT().AddLog(gomock.Any()).AnyTimes()
	stateLedger.EXPECT().SetBalance(gomock.Any(), gomock.Any()).AnyTimes()

	err := InitCouncilMembers(stateLedger, []*repo.Admin{
		{
			Address: admin1,
			Weight:  1,
			Name:    "111",
		},
		{
			Address: admin2,
			Weight:  1,
			Name:    "222",
		},
		{
			Address: admin3,
			Weight:  1,
			Name:    "333",
		},
		{
			Address: admin4,
			Weight:  1,
			Name:    "444",
		},
	}, "10000000")
	assert.Nil(t, err)

	wlpm.Reset(1, stateLedger)

	addr := types.NewAddressByStr(admin1).ETHAddress()
	_, err = wlpm.propose(&addr, &WhiteListProviderProposalArgs{
		BaseProposalArgs: BaseProposalArgs{
			ProposalType: uint8(WhiteListProviderAdd),
			Title:        "title",
			Desc:         "desc",
			BlockNumber:  1000,
		},
		WhiteListProviderArgs: access.WhiteListProviderArgs{
			Providers: []access.WhiteListProvider{
				{
					WhiteListProviderAddr: WhiteListProvider1,
				},
				{
					WhiteListProviderAddr: WhiteListProvider2,
				},
			},
		},
	})
	assert.Nil(t, err)

	testcases := []struct {
		Caller   string
		Data     []byte
		Expected vm.ExecutionResult
		Err      error
	}{
		{
			Caller: admin2,
			Data:   generateProviderVoteData(t, wlpm.proposalID.GetID()-1, Pass),
			Expected: vm.ExecutionResult{
				UsedGas: common.CalculateDynamicGas(generateProviderVoteData(t, wlpm.proposalID.GetID()-1, Pass)),
				ReturnData: generateManagerReturnData(t, &TestWhiteListProviderProposal{
					ID:          1,
					Type:        WhiteListProviderAdd,
					Proposer:    admin1,
					TotalVotes:  4,
					PassVotes:   []string{admin1, admin2},
					RejectVotes: nil,
					Status:      Voting,
					Providers: []access.WhiteListProvider{
						{
							WhiteListProviderAddr: WhiteListProvider1,
						},
						{
							WhiteListProviderAddr: WhiteListProvider2,
						},
					},
				}),
			},
			Err: nil,
		},
		{
			Caller: admin3,
			Data:   generateProviderVoteData(t, wlpm.proposalID.GetID()-1, Pass),
			Expected: vm.ExecutionResult{
				UsedGas: common.CalculateDynamicGas(generateProviderVoteData(t, wlpm.proposalID.GetID()-1, Pass)),
				ReturnData: generateManagerReturnData(t, &TestWhiteListProviderProposal{
					ID:          1,
					Type:        WhiteListProviderAdd,
					Proposer:    admin1,
					TotalVotes:  4,
					PassVotes:   []string{admin1, admin2, admin3},
					RejectVotes: nil,
					Status:      Approved,
					Providers: []access.WhiteListProvider{
						{
							WhiteListProviderAddr: WhiteListProvider1,
						},
						{
							WhiteListProviderAddr: WhiteListProvider2,
						},
					},
				}),
			},
			Err: nil,
		},
		{
			Caller: "0xfff0000000000000000000000000000000000000",
			Data:   generateProviderVoteData(t, wlpm.proposalID.GetID()-1, Pass),
			Expected: vm.ExecutionResult{
				UsedGas: common.CalculateDynamicGas(generateProviderVoteData(t, wlpm.proposalID.GetID()-1, Pass)),
				Err:     ErrNotFoundCouncilMember,
			},
			Err: ErrNotFoundCouncilMember,
		},
	}

	for _, test := range testcases {
		wlpm.Reset(1, stateLedger)

		result, err := wlpm.Run(&vm.Message{
			From: types.NewAddressByStr(test.Caller).ETHAddress(),
			Data: test.Data,
		})
		assert.Equal(t, test.Err, err)

		if result != nil {
			assert.Equal(t, nil, result.Err)
			assert.Equal(t, test.Expected.UsedGas, result.UsedGas)

			expectedProposal := &WhiteListProviderProposal{}
			err = json.Unmarshal(test.Expected.ReturnData, expectedProposal)
			assert.Nil(t, err)

			actualProposal := &WhiteListProviderProposal{}
			err = json.Unmarshal(result.ReturnData, actualProposal)
			assert.Nil(t, err)
			assert.Equal(t, *expectedProposal, *actualProposal)
		}
	}
}

func TestWhiteListProviderManager_RunForVoteRemove(t *testing.T) {
	wlpm := NewWhiteListProviderManager(&common.SystemContractConfig{
		Logger: logrus.New(),
	})

	mockCtl := gomock.NewController(t)
	stateLedger := mock_ledger.NewMockStateLedger(mockCtl)

	account := ledger.NewMockAccount(1, types.NewAddressByStr(common.WhiteListProviderManagerContractAddr))

	stateLedger.EXPECT().GetOrCreateAccount(gomock.Any()).Return(account).AnyTimes()
	stateLedger.EXPECT().AddLog(gomock.Any()).AnyTimes()
	stateLedger.EXPECT().SetBalance(gomock.Any(), gomock.Any()).AnyTimes()

	err := InitCouncilMembers(stateLedger, []*repo.Admin{
		{
			Address: admin1,
			Weight:  1,
			Name:    "111",
		},
		{
			Address: admin2,
			Weight:  1,
			Name:    "222",
		},
		{
			Address: admin3,
			Weight:  1,
			Name:    "333",
		},
		{
			Address: admin4,
			Weight:  1,
			Name:    "444",
		},
	}, "10000000")
	assert.Nil(t, err)

	wlpm.Reset(1, stateLedger)
	err = access.InitProvidersAndWhiteList(stateLedger, []string{WhiteListProvider1, WhiteListProvider2, admin1, admin2, admin3}, []string{WhiteListProvider1, WhiteListProvider2, admin1})
	assert.Nil(t, err)

	addr := types.NewAddressByStr(admin1).ETHAddress()
	_, err = wlpm.propose(&addr, &WhiteListProviderProposalArgs{
		BaseProposalArgs: BaseProposalArgs{
			ProposalType: uint8(WhiteListProviderRemove),
			Title:        "title",
			Desc:         "desc",
			BlockNumber:  1000,
		},
		WhiteListProviderArgs: access.WhiteListProviderArgs{
			Providers: []access.WhiteListProvider{
				{
					WhiteListProviderAddr: WhiteListProvider1,
				},
				{
					WhiteListProviderAddr: WhiteListProvider2,
				},
			},
		},
	})
	assert.Nil(t, err)

	testcases := []struct {
		Caller   string
		Data     []byte
		Expected vm.ExecutionResult
		Err      error
	}{
		{
			Caller: admin2,
			Data:   generateProviderVoteData(t, wlpm.proposalID.GetID()-1, Pass),
			Expected: vm.ExecutionResult{
				UsedGas: common.CalculateDynamicGas(generateProviderVoteData(t, wlpm.proposalID.GetID()-1, Pass)),
				ReturnData: generateManagerReturnData(t, &TestWhiteListProviderProposal{
					ID:          1,
					Type:        WhiteListProviderRemove,
					Proposer:    admin1,
					TotalVotes:  4,
					PassVotes:   []string{admin1, admin2},
					RejectVotes: nil,
					Status:      Voting,
					Providers: []access.WhiteListProvider{
						{
							WhiteListProviderAddr: WhiteListProvider1,
						},
						{
							WhiteListProviderAddr: WhiteListProvider2,
						},
					},
				}),
			},
			Err: nil,
		},
		{
			Caller: admin3,
			Data:   generateProviderVoteData(t, wlpm.proposalID.GetID()-1, Pass),
			Expected: vm.ExecutionResult{
				UsedGas: common.CalculateDynamicGas(generateProviderVoteData(t, wlpm.proposalID.GetID()-1, Pass)),
				ReturnData: generateManagerReturnData(t, &TestWhiteListProviderProposal{
					ID:          1,
					Type:        WhiteListProviderRemove,
					Proposer:    admin1,
					TotalVotes:  4,
					PassVotes:   []string{admin1, admin2, admin3},
					RejectVotes: nil,
					Status:      Approved,
					Providers: []access.WhiteListProvider{
						{
							WhiteListProviderAddr: WhiteListProvider1,
						},
						{
							WhiteListProviderAddr: WhiteListProvider2,
						},
					},
				}),
			},
			Err: nil,
		},
	}

	for _, test := range testcases {
		// wlpm.Reset(stateLedger)
		result, err := wlpm.Run(&vm.Message{
			From: types.NewAddressByStr(test.Caller).ETHAddress(),
			Data: test.Data,
		})
		assert.Equal(t, test.Err, err)

		if result != nil {
			assert.Equal(t, nil, result.Err)
			assert.Equal(t, test.Expected.UsedGas, result.UsedGas)

			expectedProposal := &WhiteListProviderProposal{}
			err = json.Unmarshal(test.Expected.ReturnData, expectedProposal)
			assert.Nil(t, err)

			actualProposal := &WhiteListProviderProposal{}
			err = json.Unmarshal(result.ReturnData, actualProposal)
			assert.Nil(t, err)
			assert.Equal(t, *expectedProposal, *actualProposal)
		}
	}
}

func TestWhiteListProviderManager_EstimateGas(t *testing.T) {
	wlpm := NewWhiteListProviderManager(&common.SystemContractConfig{
		Logger: logrus.New(),
	})

	from := types.NewAddressByStr(admin1).ETHAddress()
	to := types.NewAddressByStr(common.WhiteListProviderManagerContractAddr).ETHAddress()
	data := hexutil.Bytes(generateProviderProposeData(t, WhiteListProviderAdd, access.WhiteListProviderArgs{
		Providers: []access.WhiteListProvider{
			{
				WhiteListProviderAddr: WhiteListProvider1,
			},
			{
				WhiteListProviderAddr: WhiteListProvider2,
			},
		},
	}))
	// test propose
	gas, err := wlpm.EstimateGas(&types.CallArgs{
		From: &from,
		To:   &to,
		Data: &data,
	})
	assert.Nil(t, err)
	intrinsicGas, _ := vm.IntrinsicGas(data, []ethtype.AccessTuple{}, false, true, true, true)
	assert.Equal(t, intrinsicGas, gas)

	// test vote
	data = generateProviderVoteData(t, 1, Pass)
	gas, err = wlpm.EstimateGas(&types.CallArgs{
		From: &from,
		To:   &to,
		Data: &data,
	})
	assert.Nil(t, err)
	intrinsicGas, _ = vm.IntrinsicGas(data, []ethtype.AccessTuple{}, false, true, true, true)
	assert.Equal(t, intrinsicGas, gas)

	// test error args
	data = []byte{0, 1, 2, 3}
	gas, err = wlpm.EstimateGas(&types.CallArgs{
		From: &from,
		To:   &to,
		Data: &data,
	})
	assert.NotNil(t, err)
	assert.Equal(t, uint64(0), gas)
}

func generateProviderProposeData(t *testing.T, proposalType ProposalType, extraArgs access.WhiteListProviderArgs) []byte {
	gabi, err := GetABI()

	title := "title"
	desc := "desc"
	blockNumber := uint64(1000)
	extra, err := json.Marshal(extraArgs)
	assert.Nil(t, err)
	data, err := gabi.Pack(ProposeMethod, uint8(proposalType), title, desc, blockNumber, extra)
	assert.Nil(t, err)

	return data
}

func generateProviderVoteData(t *testing.T, proposalID uint64, voteResult VoteResult) []byte {
	gabi, err := GetABI()

	data, err := gabi.Pack(VoteMethod, proposalID, voteResult, []byte(""))
	assert.Nil(t, err)

	return data
}

func generateManagerReturnData(t *testing.T, testProposal *TestWhiteListProviderProposal) []byte {
	proposal := &WhiteListProviderProposal{
		BaseProposal: BaseProposal{
			ID:          testProposal.ID,
			Type:        testProposal.Type,
			Strategy:    NowProposalStrategy,
			Proposer:    testProposal.Proposer,
			Title:       "title",
			Desc:        "desc",
			BlockNumber: uint64(1000),
			TotalVotes:  testProposal.TotalVotes,
			PassVotes:   testProposal.PassVotes,
			RejectVotes: testProposal.RejectVotes,
			Status:      testProposal.Status,
		},
		WhiteListProviderArgs: access.WhiteListProviderArgs{
			Providers: testProposal.Providers,
		},
	}

	b, err := json.Marshal(proposal)
	assert.Nil(t, err)

	return b
}

func TestWhiteListProviderManager_loadProviderProposal(t *testing.T) {
	wlpm := NewWhiteListProviderManager(&common.SystemContractConfig{
		Logger: logrus.New(),
	})

	mockCtl := gomock.NewController(t)
	stateLedger := mock_ledger.NewMockStateLedger(mockCtl)

	account := ledger.NewMockAccount(1, types.NewAddressByStr(common.WhiteListProviderManagerContractAddr))

	stateLedger.EXPECT().GetOrCreateAccount(gomock.Any()).Return(account).AnyTimes()
	wlpm.Reset(1, stateLedger)
	_, err := wlpm.loadProviderProposal(1)
	assert.Equal(t, errors.New("provider proposal not found for the id"), err)

	proposalID := uint64(1)
	account.SetState([]byte(fmt.Sprintf("%s%d", WhiteListProviderProposalKey, proposalID)), []byte{1, 2, 3, 4})
	// test unmarshal fail
	_, err = wlpm.loadProviderProposal(proposalID)
	assert.NotNil(t, err)
}

func TestWhiteListProviderManager_checkFinishedProposal_providerProposal(t *testing.T) {
	wlpm := NewWhiteListProviderManager(&common.SystemContractConfig{
		Logger: logrus.New(),
	})

	mockCtl := gomock.NewController(t)
	stateLedger := mock_ledger.NewMockStateLedger(mockCtl)

	account := ledger.NewMockAccount(1, types.NewAddressByStr(common.WhiteListProviderManagerContractAddr))

	stateLedger.EXPECT().GetOrCreateAccount(gomock.Any()).Return(account).AnyTimes()
	stateLedger.EXPECT().AddLog(gomock.Any()).AnyTimes()
	stateLedger.EXPECT().SetBalance(gomock.Any(), gomock.Any()).AnyTimes()

	err := InitCouncilMembers(stateLedger, []*repo.Admin{
		{
			Address: admin1,
			Weight:  1,
			Name:    "111",
		},
		{
			Address: admin2,
			Weight:  1,
			Name:    "222",
		},
		{
			Address: admin3,
			Weight:  1,
			Name:    "333",
		},
		{
			Address: admin4,
			Weight:  1,
			Name:    "444",
		},
	}, "10")
	assert.Nil(t, err)

	err = access.InitProvidersAndWhiteList(stateLedger, []string{admin1, admin2, admin3}, []string{admin1, admin2, admin3})
	assert.Nil(t, err)

	wlpm.Reset(1, stateLedger)
	addr := types.NewAddressByStr(admin1).ETHAddress()
	_, err = wlpm.propose(&addr, &WhiteListProviderProposalArgs{
		BaseProposalArgs: BaseProposalArgs{
			ProposalType: uint8(WhiteListProviderAdd),
			Title:        "test",
			Desc:         "test",
			BlockNumber:  10,
		},
		WhiteListProviderArgs: access.WhiteListProviderArgs{
			Providers: []access.WhiteListProvider{
				{
					WhiteListProviderAddr: WhiteListProvider1,
				},
			},
		},
	})
	assert.Nil(t, err)

	_, err = wlpm.propose(&addr, &WhiteListProviderProposalArgs{
		BaseProposalArgs: BaseProposalArgs{
			ProposalType: uint8(WhiteListProviderAdd),
			Title:        "test",
			Desc:         "test",
			BlockNumber:  10,
		},
		WhiteListProviderArgs: access.WhiteListProviderArgs{
			Providers: []access.WhiteListProvider{
				{
					WhiteListProviderAddr: WhiteListProvider2,
				},
			},
		},
	})
	assert.Equal(t, ErrExistVotingProposal, err)
}

func TestWhiteListProviderManager_checkFinishedProposal_councilProposal(t *testing.T) {
	wlpm := NewWhiteListProviderManager(&common.SystemContractConfig{
		Logger: logrus.New(),
	})

	mockCtl := gomock.NewController(t)
	stateLedger := mock_ledger.NewMockStateLedger(mockCtl)

	account := ledger.NewMockAccount(1, types.NewAddressByStr(common.WhiteListProviderManagerContractAddr))

	stateLedger.EXPECT().GetOrCreateAccount(gomock.Any()).Return(account).AnyTimes()

	notFinishedProposalMgr := NewNotFinishedProposalMgr(stateLedger)
	err := notFinishedProposalMgr.SetProposal(&NotFinishedProposal{
		ID:                  1,
		DeadlineBlockNumber: 10,
		ContractAddr:        common.CouncilManagerContractAddr,
	})
	assert.Nil(t, err)

	wlpm.Reset(1, stateLedger)
	_, err = wlpm.checkFinishedProposal()
	assert.Equal(t, ErrExistVotingProposal, err)
}

func TestWhiteListProviderManager_propose(t *testing.T) {
	wlpm := NewWhiteListProviderManager(&common.SystemContractConfig{
		Logger: logrus.New(),
	})

	mockCtl := gomock.NewController(t)
	stateLedger := mock_ledger.NewMockStateLedger(mockCtl)

	account := ledger.NewMockAccount(1, types.NewAddressByStr(common.WhiteListProviderManagerContractAddr))

	stateLedger.EXPECT().GetOrCreateAccount(gomock.Any()).Return(account).AnyTimes()
	stateLedger.EXPECT().AddLog(gomock.Any()).AnyTimes()
	stateLedger.EXPECT().SetBalance(gomock.Any(), gomock.Any()).AnyTimes()
	wlpm.Reset(1, stateLedger)

	err := InitCouncilMembers(stateLedger, []*repo.Admin{
		{
			Address: admin1,
			Weight:  1,
			Name:    "111",
		},
		{
			Address: admin2,
			Weight:  1,
			Name:    "222",
		},
		{
			Address: admin3,
			Weight:  1,
			Name:    "333",
		},
		{
			Address: admin4,
			Weight:  1,
			Name:    "444",
		},
	}, "10")
	assert.Nil(t, err)

	err = access.InitProvidersAndWhiteList(stateLedger, []string{admin1, admin2, admin3}, []string{admin1, admin2, admin3})
	assert.Nil(t, err)

	testcases := []struct {
		from     ethcommon.Address
		args     *WhiteListProviderProposalArgs
		expected error
	}{
		{
			from: types.NewAddressByStr(admin1).ETHAddress(),
			args: &WhiteListProviderProposalArgs{
				BaseProposalArgs: BaseProposalArgs{
					ProposalType: uint8(WhiteListProviderAdd),
					Title:        "title",
					Desc:         "desc",
					BlockNumber:  10,
				},
				WhiteListProviderArgs: access.WhiteListProviderArgs{
					Providers: []access.WhiteListProvider{
						{
							WhiteListProviderAddr: admin1,
						},
					},
				},
			},
			expected: fmt.Errorf("provider already exists, %s", admin1),
		},
		{
			from: types.NewAddressByStr(admin2).ETHAddress(),
			args: &WhiteListProviderProposalArgs{
				BaseProposalArgs: BaseProposalArgs{
					ProposalType: uint8(WhiteListProviderRemove),
					Title:        "title",
					Desc:         "desc",
					BlockNumber:  10,
				},
				WhiteListProviderArgs: access.WhiteListProviderArgs{
					Providers: []access.WhiteListProvider{
						{
							WhiteListProviderAddr: "0x0000000000000000000000000000000000000000",
						},
					},
				},
			},
			expected: fmt.Errorf("provider does not exist, %s", "0x0000000000000000000000000000000000000000"),
		},
		{
			from: types.NewAddressByStr(admin3).ETHAddress(),
			args: &WhiteListProviderProposalArgs{
				BaseProposalArgs: BaseProposalArgs{
					ProposalType: uint8(WhiteListProviderAdd),
					Title:        "title",
					Desc:         "desc",
					BlockNumber:  10,
				},
				WhiteListProviderArgs: access.WhiteListProviderArgs{
					Providers: []access.WhiteListProvider{},
				},
			},
			expected: errors.New("empty providers"),
		},
		{
			from: types.NewAddressByStr(admin3).ETHAddress(),
			args: &WhiteListProviderProposalArgs{
				BaseProposalArgs: BaseProposalArgs{
					ProposalType: uint8(WhiteListProviderAdd),
					Title:        "title",
					Desc:         "desc",
					BlockNumber:  10,
				},
				WhiteListProviderArgs: access.WhiteListProviderArgs{
					Providers: []access.WhiteListProvider{
						{
							WhiteListProviderAddr: "0x0000000000000000000000000000000000000000",
						},
						{
							WhiteListProviderAddr: "0x0000000000000000000000000000000000000000",
						},
					},
				},
			},
			expected: errors.New("provider address repeated"),
		},
	}

	for _, testcase := range testcases {
		_, err := wlpm.propose(&testcase.from, testcase.args)
		assert.Equal(t, testcase.expected, err)
	}

	// test unfinished proposal
	addr := types.NewAddressByStr(admin1).ETHAddress()
	_, err = wlpm.propose(&addr, &WhiteListProviderProposalArgs{
		BaseProposalArgs: BaseProposalArgs{
			ProposalType: uint8(WhiteListProviderAdd),
			Title:        "title",
			Desc:         "desc",
			BlockNumber:  10,
		},
		WhiteListProviderArgs: access.WhiteListProviderArgs{
			Providers: []access.WhiteListProvider{
				{
					WhiteListProviderAddr: WhiteListProvider1,
				},
			},
		},
	})
	assert.Nil(t, err)

	testcases2 := []struct {
		from     ethcommon.Address
		args     *WhiteListProviderProposalArgs
		expected error
	}{
		{
			from: types.NewAddressByStr(admin1).ETHAddress(),
			args: &WhiteListProviderProposalArgs{
				BaseProposalArgs: BaseProposalArgs{
					ProposalType: 4,
					Title:        "title",
					Desc:         "desc",
					BlockNumber:  10,
				},
				WhiteListProviderArgs: access.WhiteListProviderArgs{
					Providers: []access.WhiteListProvider{
						{
							WhiteListProviderAddr: WhiteListProvider1,
						},
					},
				},
			},
			expected: ErrExistVotingProposal,
		},
	}
	_, err = wlpm.propose(&testcases2[0].from, testcases2[0].args)
	assert.Equal(t, testcases2[0].expected, err)
}

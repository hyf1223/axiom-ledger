package contracts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/meshplus/bitxhub-core/boltvm"
	"github.com/meshplus/bitxhub-core/eth-contracts/escrows-contracts"
	"github.com/meshplus/bitxhub-core/eth-contracts/interchain-contracts"
	"github.com/meshplus/bitxhub-core/eth-contracts/proxy-contracts"
	"github.com/meshplus/bitxhub-model/constant"
	"github.com/meshplus/bitxhub-model/pb"
	"github.com/meshplus/bitxhub/internal/executor/oracle/appchain"
)

const (
	EscrowsAddrKey        = "escrows_addr_key"
	InterchainSwapAddrKey = "interchain_swap_addr_key"
	ProxyAddrKey          = "proxy_addr_key"
	EthTxHashPrefix       = "eth-hash"
)

type ContractAddr struct {
	Addr string `json:"addr"`
}

type EthHeaderManager struct {
	boltvm.Stub
	oracle *appchain.EthLightChainOracle
}

func NewEthHeaderManager(ropstenOracle *appchain.EthLightChainOracle) *EthHeaderManager {
	log.Root().SetHandler(log.LvlFilterHandler(log.LvlError, log.StreamHandler(os.Stderr, log.TerminalFormat(false))))
	return &EthHeaderManager{oracle: ropstenOracle}
}

func (ehm *EthHeaderManager) SetEscrowAddr(pierAddr string, addr string) *boltvm.Response {
	if res := ehm.IsAdmin(); !res.Ok {
		return res
	}
	ok := common.IsHexAddress(addr)
	if !ok {
		return boltvm.Error("addr formot error")
	}
	res := ehm.CrossInvoke(constant.AppchainMgrContractAddr.String(), "GetIdByAddr", pb.String(pierAddr))
	if !res.Ok {
		return boltvm.Error(string(res.Result))
	}
	escrowsAddr := ContractAddr{addr}
	ehm.SetObject(EscrowsAddrKey+pierAddr, escrowsAddr)

	return boltvm.Success([]byte(addr))
}

func (ehm *EthHeaderManager) GetEscrowAddr(pierAddr string) *boltvm.Response {
	var escrowsAddr ContractAddr
	res := ehm.CrossInvoke(constant.AppchainMgrContractAddr.String(), "GetIdByAddr", pb.String(pierAddr))
	if !res.Ok {
		return boltvm.Error(string(res.Result))
	}
	ok := ehm.GetObject(EscrowsAddrKey+pierAddr, &escrowsAddr)
	if ok {
		return boltvm.Success([]byte(escrowsAddr.Addr))
	}
	return boltvm.Error("not found")
}

func (ehm *EthHeaderManager) SetProxyAddr(addr string) *boltvm.Response {
	if res := ehm.IsAdmin(); !res.Ok {
		return res
	}
	ok := common.IsHexAddress(addr)
	if ok {
		proxyAddr := &ContractAddr{addr}
		ehm.SetObject(ProxyAddrKey, proxyAddr)
	}
	return boltvm.Success([]byte(addr))
}

func (ehm *EthHeaderManager) SetInterchainSwapAddr(addr string) *boltvm.Response {
	if res := ehm.IsAdmin(); !res.Ok {
		return res
	}
	ok := common.IsHexAddress(addr)
	if ok {
		interchainSwapAddr := &ContractAddr{addr}
		ehm.SetObject(InterchainSwapAddrKey, interchainSwapAddr)
	}
	return boltvm.Success([]byte(addr))
}

func (ehm *EthHeaderManager) GetInterchainSwapAddr() *boltvm.Response {
	var interchainSwapAddr ContractAddr
	ok := ehm.GetObject(InterchainSwapAddrKey, &interchainSwapAddr)
	if ok {
		return boltvm.Success([]byte(interchainSwapAddr.Addr))
	}
	return boltvm.Error("not found")
}

func (ehm *EthHeaderManager) InsertBlockHeaders(headersData []byte) *boltvm.Response {
	headers := make([]*types.Header, 0)
	err := json.Unmarshal(headersData, &headers)
	if err != nil {
		return boltvm.Error(err.Error())
	}
	num, err := ehm.oracle.InsertBlockHeaders(headers)
	if err != nil {
		return boltvm.Error(err.Error())
	}
	return boltvm.Success([]byte(strconv.Itoa(num)))
}

func (ehm *EthHeaderManager) CurrentBlockHeader() *boltvm.Response {
	header := ehm.oracle.CurrentHeader()
	if header == nil {
		return boltvm.Error("not found")
	}
	return boltvm.Success(header.Number.Bytes())
}

func (ehm *EthHeaderManager) GetBlockHeader(hash string) *boltvm.Response {
	header := ehm.oracle.GetHeader(common.HexToHash(hash))
	if header == nil {
		return boltvm.Error("not found")
	}
	data, err := header.MarshalJSON()
	if err != nil {
		return boltvm.Error(err.Error())
	}
	return boltvm.Success(data)
}

func (ehm *EthHeaderManager) Mint(receiptData []byte, proofData []byte) *boltvm.Response {
	var (
		interchainSwapAddr *ContractAddr
		proxyAddr          *ContractAddr
	)

	var receipt types.Receipt
	err := receipt.UnmarshalJSON(receiptData)
	if err != nil {
		return boltvm.Error(err.Error())
	}
	ok, v := ehm.Get(EthTxKey(receipt.TxHash.String()))
	if ok {
		return boltvm.Success(v)
	}

	//// for quick swap, suspend following verify logic
	//err = ehm.oracle.VerifyProof(&receipt, proofData)
	//if err != nil {
	//	return boltvm.Error(err.Error())
	//}
	escrowsLockEvent, escrowsQuickSwapEvent, err := ehm.unpackEscrowsLock(&receipt)
	if err != nil {
		return boltvm.Error(err.Error())
	}

	if escrowsQuickSwapEvent != nil {
		// do swap logic
		ok := ehm.GetObject(ProxyAddrKey, &proxyAddr)
		if !ok {
			return boltvm.Error("not found proxy contract address")
		}
		proxyAbi, err := abi.JSON(bytes.NewReader([]byte(proxy_contracts.ProxyABI)))
		if err != nil {
			return boltvm.Error(err.Error())
		}
		input, err := proxyAbi.Pack("proxy",
			escrowsLockEvent.EthToken,
			escrowsLockEvent.RelayToken,
			escrowsLockEvent.Locker,
			common.HexToAddress(escrowsLockEvent.Recipient),
			escrowsLockEvent.Amount,
			receipt.TxHash.String(),
			escrowsLockEvent.AppchainIndex,
			common.HexToAddress(escrowsQuickSwapEvent.DstChainId),
			common.HexToAddress(escrowsQuickSwapEvent.DstContract))
		if err != nil {
			return boltvm.Error(err.Error())
		}
		ehm.Logger().Info("proxy txhash is :" + ehm.GetTxHash().String())
		res := ehm.CrossInvokeEVM(proxyAddr.Addr, input)
		if res.Ok {
			ehm.Set(EthTxKey(receipt.TxHash.String()), res.Result)
		}
		return res
	} else {
		ok := ehm.GetObject(InterchainSwapAddrKey, &interchainSwapAddr)
		if !ok {
			return boltvm.Error("not found interchain swap contract address")
		}
		interchainSwapAbi, err := abi.JSON(bytes.NewReader([]byte(interchain_contracts.InterchainSwapABI)))
		if err != nil {
			return boltvm.Error(err.Error())
		}
		input, err := interchainSwapAbi.Pack("mint",
			escrowsLockEvent.EthToken, escrowsLockEvent.RelayToken, escrowsLockEvent.Locker,
			common.HexToAddress(escrowsLockEvent.Recipient), escrowsLockEvent.Amount, receipt.TxHash.String(), escrowsLockEvent.AppchainIndex)
		ehm.Logger().Info("lock txhash is :" + ehm.GetTxHash().String())
		if err != nil {
			return boltvm.Error(err.Error())
		}
		res := ehm.CrossInvokeEVM(interchainSwapAddr.Addr, input)
		if res.Ok {
			ehm.Set(EthTxKey(receipt.TxHash.String()), res.Result)
		}
		return res
	}
}

func (ehm *EthHeaderManager) GetPrefixedHash(hash string) *boltvm.Response {
	ok, v := ehm.Get(EthTxKey(hash))
	if ok {
		return boltvm.Success(v)
	}
	return boltvm.Error(fmt.Sprintf("not found hash by %v", hash))
}

func (ehm *EthHeaderManager) unpackEscrowsLock(receipt *types.Receipt) (*escrows_contracts.EscrowsLock, *escrows_contracts.EscrowsQuickSwap, error) {
	var escrowsAddr ContractAddr
	ok := ehm.GetObject(EscrowsAddrKey+ehm.Caller(), &escrowsAddr)
	if !ok {
		return nil, nil, fmt.Errorf("not found the escrows contract address")
	}
	var (
		lock *escrows_contracts.EscrowsLock
		swap *escrows_contracts.EscrowsQuickSwap
	)
	for _, log := range receipt.Logs {
		if !strings.EqualFold(log.Address.String(), escrowsAddr.Addr) {
			continue
		}

		if log.Removed {
			continue
		}
		escrows, err := escrows_contracts.NewEscrows(common.Address{}, nil)
		if err != nil {
			continue
		}
		if lock == nil {
			lock, _ = escrows.ParseLock(*log)
		}
		if swap == nil {
			swap, _ = escrows.ParseQuickSwap(*log)
		}
	}
	if lock == nil {
		return nil, nil, fmt.Errorf("not found the escrow lock event")
	}
	return lock, swap, nil
}

func (ehm *EthHeaderManager) IsAdmin() *boltvm.Response {
	ret := ehm.CrossInvoke(constant.RoleContractAddr.String(), "IsAdmin", pb.String(ehm.Caller()))
	is, err := strconv.ParseBool(string(ret.Result))
	if err != nil {
		return boltvm.Error(fmt.Errorf("judge caller type: %w", err).Error())
	}

	if !is {
		return boltvm.Error("caller is not an admin account")
	}
	return boltvm.Success([]byte("1"))
}

func EthTxKey(hash string) string {
	return fmt.Sprintf("%s-%s", EthTxHashPrefix, hash)
}

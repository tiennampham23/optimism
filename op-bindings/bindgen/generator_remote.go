package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ethereum-optimism/optimism/op-bindings/etherscan"
	"github.com/ethereum/go-ethereum/common"
)

type bindGenGeneratorRemote struct {
	bindGenGeneratorBase
	contractDataClients struct {
		eth contractDataClient
		op  contractDataClient
	}
	tempArtifactsDir string
}

type contractDataClient interface {
	FetchAbi(ctx context.Context, address string) (string, error)
	FetchDeployedBytecode(ctx context.Context, address string) (string, error)
	FetchDeploymentTxHash(ctx context.Context, address string) (string, error)
	FetchDeploymentTx(ctx context.Context, txHash string) (etherscan.TxInfo, error)
}

type deployments struct {
	Eth common.Address `json:"eth"`
	Op  common.Address `json:"op"`
}

type remoteContract struct {
	Name           string         `json:"name"`
	Verified       bool           `json:"verified"`
	Deployments    deployments    `json:"deployments"`
	DeploymentSalt string         `json:"deploymentSalt"`
	Deployer       common.Address `json:"deployer"`
	ABI            string         `json:"abi"`
	InitBytecode   string         `json:"initBytecode"`
}

type remoteContractMetadata struct {
	remoteContract
	Package     string
	InitBin     string
	DeployedBin string
}

func (generator *bindGenGeneratorRemote) generateBindings() error {
	contracts, err := readContractList(generator.logger, generator.contractsListPath)
	if err != nil {
		return fmt.Errorf("error reading contract list %s: %w", generator.contractsListPath, err)
	}
	if len(contracts.Remote) == 0 {
		return fmt.Errorf("no contracts parsed from given contract list: %s", generator.contractsListPath)
	}

	return generator.processContracts(contracts.Remote)
}

func (generator *bindGenGeneratorRemote) processContracts(contracts []remoteContract) error {
	var err error
	generator.tempArtifactsDir, err = mkTempArtifactsDir(generator.logger)
	if err != nil {
		return err
	}
	defer func() {
		err := os.RemoveAll(generator.tempArtifactsDir)
		if err != nil {
			generator.logger.Error("Error removing temporary artifact directory", "path", generator.tempArtifactsDir, "err", err.Error())
		} else {
			generator.logger.Debug("Successfully removed temporary artifact directory")
		}
	}()

	for _, contract := range contracts {
		generator.logger.Info("Generating bindings and metadata for remote contract", "contract", contract.Name)

		contractMetadata := remoteContractMetadata{
			remoteContract: remoteContract{
				Name:           contract.Name,
				Deployments:    contract.Deployments,
				DeploymentSalt: contract.DeploymentSalt,
				ABI:            contract.ABI,
				Verified:       contract.Verified,
			},
			Package: generator.bindingsPackageName,
		}

		var err error
		switch contract.Name {
		case "MultiCall3", "Safe_v130", "SafeL2_v130", "MultiSendCallOnly_v130",
			"EntryPoint", "SafeSingletonFactory", "DeterministicDeploymentProxy":
			err = generator.standardHandler(&contractMetadata)
		case "Create2Deployer":
			err = generator.create2DeployerHandler(&contractMetadata)
		case "MultiSend_v130":
			err = generator.multiSendHandler(&contractMetadata)
		case "SenderCreator":
			// The SenderCreator contract is deployed by EntryPoint, so the transaction data
			// from the deployment transaction is for the entire EntryPoint deployment.
			// So, we're manually providing the initialization bytecode
			contractMetadata.InitBin = contract.InitBytecode
			err = generator.senderCreatorHandler(&contractMetadata)
		case "Permit2":
			// Permit2 has an immutable Solidity variable that resolves to block.chainid,
			// so we can't use the deployed bytecode, and instead must generate it
			// at some later point not handled by BindGen.
			// DeployerAddress is intended to be used to help deploy Permit2 at it's deterministic address
			// to a chain set with the required id to be able to obtain a diff minimized deployed bytecode
			contractMetadata.Deployer = contract.Deployer
			err = generator.permit2Handler(&contractMetadata)
		default:
			err = fmt.Errorf("unknown contract: %s, don't know how to handle it", contract.Name)
		}

		if err != nil {
			return err
		}
	}

	return nil
}

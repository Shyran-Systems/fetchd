package cli

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/context"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/x/auth"
	"github.com/cosmos/cosmos-sdk/x/auth/client/utils"

	wasmUtils "github.com/fetchai/fetchd/x/wasm/client/utils"
	"github.com/fetchai/fetchd/x/wasm/internal/types"
)

const (
	flagAmount                 = "amount"
	flagSource                 = "source"
	flagBuilder                = "builder"
	flagLabel                  = "label"
	flagAdmin                  = "admin"
	flagRunAs                  = "run-as"
	flagInstantiateByEverybody = "instantiate-everybody"
	flagInstantiateByAddress   = "instantiate-only-address"
	flagProposalType           = "type"
)

// GetTxCmd returns the transaction commands for this module
func GetTxCmd(cdc *codec.Codec) *cobra.Command {
	txCmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Wasm transaction subcommands",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	txCmd.AddCommand(flags.PostCommands(
		StoreCodeCmd(cdc),
		InstantiateContractCmd(cdc),
		ExecuteContractCmd(cdc),
		MigrateContractCmd(cdc),
		UpdateContractAdminCmd(cdc),
		ClearContractAdminCmd(cdc),
	)...)
	return txCmd
}

// StoreCodeCmd will upload code to be reused.
func StoreCodeCmd(cdc *codec.Codec) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "store [wasm file] --source [source] --builder [builder]",
		Short: "Upload a wasm binary",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			inBuf := bufio.NewReader(cmd.InOrStdin())
			txBldr := auth.NewTxBuilderFromCLI(inBuf).WithTxEncoder(utils.GetTxEncoder(cdc))
			cliCtx := context.NewCLIContextWithInput(inBuf).WithCodec(cdc)

			msg, err := parseStoreCodeArgs(args, cliCtx)
			if err != nil {
				return err
			}
			if err = msg.ValidateBasic(); err != nil {
				return err
			}

			return utils.GenerateOrBroadcastMsgs(cliCtx, txBldr, []sdk.Msg{msg})
		},
	}

	cmd.Flags().String(flagSource, "", "A valid URI reference to the contract's source code, optional")
	cmd.Flags().String(flagBuilder, "", "A valid docker tag for the build system, optional")
	cmd.Flags().String(flagInstantiateByEverybody, "", "Everybody can instantiate a contract from the code, optional")
	cmd.Flags().String(flagInstantiateByAddress, "", "Only this address can instantiate a contract instance from the code, optional")

	return cmd
}

func parseStoreCodeArgs(args []string, cliCtx context.CLIContext) (types.MsgStoreCode, error) {
	wasm, err := ioutil.ReadFile(args[0])
	if err != nil {
		return types.MsgStoreCode{}, err
	}

	// gzip the wasm file
	if wasmUtils.IsWasm(wasm) {
		wasm, err = wasmUtils.GzipIt(wasm)

		if err != nil {
			return types.MsgStoreCode{}, err
		}
	} else if !wasmUtils.IsGzip(wasm) {
		return types.MsgStoreCode{}, fmt.Errorf("invalid input file. Use wasm binary or gzip")
	}

	var perm *types.AccessConfig
	if onlyAddrStr := viper.GetString(flagInstantiateByAddress); onlyAddrStr != "" {
		allowedAddr, err := sdk.AccAddressFromBech32(onlyAddrStr)
		if err != nil {
			return types.MsgStoreCode{}, sdkerrors.Wrap(err, flagInstantiateByAddress)
		}
		x := types.OnlyAddress.With(allowedAddr)
		perm = &x
	} else if everybody := viper.GetBool(flagInstantiateByEverybody); everybody {
		perm = &types.AllowEverybody
	}

	// build and sign the transaction, then broadcast to Tendermint
	msg := types.MsgStoreCode{
		Sender:                cliCtx.GetFromAddress(),
		WASMByteCode:          wasm,
		Source:                viper.GetString(flagSource),
		Builder:               viper.GetString(flagBuilder),
		InstantiatePermission: perm,
	}
	return msg, nil
}

// InstantiateContractCmd will instantiate a contract from previously uploaded code.
func InstantiateContractCmd(cdc *codec.Codec) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "instantiate [code_id_int64] [json_encoded_init_args] --label [text] --admin [address,optional] --amount [coins,optional]",
		Short: "Instantiate a wasm contract",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			inBuf := bufio.NewReader(cmd.InOrStdin())
			txBldr := auth.NewTxBuilderFromCLI(inBuf).WithTxEncoder(utils.GetTxEncoder(cdc))
			cliCtx := context.NewCLIContextWithInput(inBuf).WithCodec(cdc)

			msg, err := parseInstantiateArgs(args, cliCtx)
			if err != nil {
				return err
			}
			if err := msg.ValidateBasic(); err != nil {
				return err
			}
			return utils.GenerateOrBroadcastMsgs(cliCtx, txBldr, []sdk.Msg{msg})
		},
	}

	cmd.Flags().String(flagAmount, "", "Coins to send to the contract during instantiation")
	cmd.Flags().String(flagLabel, "", "A human-readable name for this contract in lists")
	cmd.Flags().String(flagAdmin, "", "Address of an admin")
	return cmd
}

func parseInstantiateArgs(args []string, cliCtx context.CLIContext) (types.MsgInstantiateContract, error) {
	// get the id of the code to instantiate
	codeID, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return types.MsgInstantiateContract{}, err
	}

	amounstStr := viper.GetString(flagAmount)
	amount, err := sdk.ParseCoins(amounstStr)
	if err != nil {
		return types.MsgInstantiateContract{}, err
	}

	label := viper.GetString(flagLabel)
	if label == "" {
		return types.MsgInstantiateContract{}, fmt.Errorf("Label is required on all contracts")
	}

	initMsg := args[1]

	adminStr := viper.GetString(flagAdmin)
	var adminAddr sdk.AccAddress
	if len(adminStr) != 0 {
		adminAddr, err = sdk.AccAddressFromBech32(adminStr)
		if err != nil {
			return types.MsgInstantiateContract{}, sdkerrors.Wrap(err, "admin")
		}
	}

	// build and sign the transaction, then broadcast to Tendermint
	msg := types.MsgInstantiateContract{
		Sender:    cliCtx.GetFromAddress(),
		CodeID:    codeID,
		Label:     label,
		InitFunds: amount,
		InitMsg:   []byte(initMsg),
		Admin:     adminAddr,
	}
	return msg, nil
}

// ExecuteContractCmd will instantiate a contract from previously uploaded code.
func ExecuteContractCmd(cdc *codec.Codec) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "execute [contract_addr_bech32] [json_encoded_send_args]",
		Short: "Execute a command on a wasm contract",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			inBuf := bufio.NewReader(cmd.InOrStdin())
			txBldr := auth.NewTxBuilderFromCLI(inBuf).WithTxEncoder(utils.GetTxEncoder(cdc))
			cliCtx := context.NewCLIContextWithInput(inBuf).WithCodec(cdc)

			// get the id of the code to instantiate
			contractAddr, err := sdk.AccAddressFromBech32(args[0])
			if err != nil {
				return err
			}

			amounstStr := viper.GetString(flagAmount)
			amount, err := sdk.ParseCoins(amounstStr)
			if err != nil {
				return err
			}

			execMsg := args[1]

			// build and sign the transaction, then broadcast to Tendermint
			msg := types.MsgExecuteContract{
				Sender:    cliCtx.GetFromAddress(),
				Contract:  contractAddr,
				SentFunds: amount,
				Msg:       []byte(execMsg),
			}
			return utils.GenerateOrBroadcastMsgs(cliCtx, txBldr, []sdk.Msg{msg})
		},
	}

	cmd.Flags().String(flagAmount, "", "Coins to send to the contract along with command")
	return cmd
}

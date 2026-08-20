package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/s0up4200/bluepost/internal/bus"
	"github.com/s0up4200/bluepost/internal/model"
	"github.com/s0up4200/bluepost/internal/protocol"
	"github.com/s0up4200/bluepost/internal/textsafe"
)

func New(client bus.API, stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "bluepost",
		Short:         "Read iPhone messages and contacts through BlueZ",
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(statusCommand(client), messagesCommand(client), contactsCommand(client))
	return root
}

func statusCommand(client bus.API) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon, storage, MAP, and PBAP status",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			command.Root().SilenceUsage = true
			ctx, cancel := context.WithTimeout(command.Context(), 10*time.Second)
			status, err := client.Status(ctx)
			cancel()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(
				command.OutOrStdout(),
				"State: %s\nStorage: %s\nMAP: %s\nPBAP: %s\nPhone: %s\nMessages: %d\nContacts: %d\nDetail: %s\n",
				textsafe.OneLine(status.State),
				textsafe.OneLine(status.Storage),
				connectionWord(status.MAP),
				connectionWord(status.PBAP),
				textsafe.OneLine(status.Phone),
				status.HistoryCount,
				status.ContactCount,
				textsafe.OneLine(status.Detail),
			)
			return err
		},
	}
}

func messagesCommand(client bus.API) *cobra.Command {
	var limit int
	var iphone bool
	command := &cobra.Command{
		Use:   "messages",
		Short: "Show recent messages",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			maximum := protocol.MaxHistoryRecords
			if iphone {
				maximum = protocol.MaxRecentRecords
			}
			if limit < 1 || limit > maximum {
				return fmt.Errorf("--limit must be between 1 and %d", maximum)
			}
			command.Root().SilenceUsage = true
			timeout := 20 * time.Second
			if iphone {
				timeout = 60 * time.Second
			}
			ctx, cancel := context.WithTimeout(command.Context(), timeout)
			var messages []model.Message
			var err error
			if iphone {
				messages, err = client.Recent(ctx, "telecom/msg/inbox", uint32(limit))
			} else {
				messages, err = client.Events(ctx, []string{"sms_received"}, uint32(limit))
			}
			cancel()
			if err != nil {
				return err
			}
			return printMessages(command.OutOrStdout(), messages)
		},
	}
	command.Flags().IntVarP(&limit, "limit", "n", 20, "maximum number of messages")
	command.Flags().BoolVar(&iphone, "iphone", false, "query the current iPhone inbox")
	return command
}

func contactsCommand(client bus.API) *cobra.Command {
	command := &cobra.Command{
		Use:   "contacts [query]",
		Short: "List or search synchronized contacts",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			command.Root().SilenceUsage = true
			ctx, cancel := context.WithTimeout(command.Context(), 8*time.Second)
			var contacts []model.Contact
			var err error
			if len(args) == 1 {
				contacts, err = client.FindContacts(ctx, args[0])
			} else {
				contacts, err = client.Contacts(ctx, 0, protocol.MaxContactPage)
			}
			cancel()
			if err != nil {
				return err
			}
			return printContacts(command.OutOrStdout(), contacts)
		},
	}
	command.AddCommand(&cobra.Command{
		Use:   "sync",
		Short: "Replace the local contact snapshot from the iPhone",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			command.Root().SilenceUsage = true
			ctx, cancel := context.WithTimeout(command.Context(), 30*time.Minute)
			count, err := client.SyncContacts(ctx)
			cancel()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "%d contacts synchronized\n", count)
			return err
		},
	})
	return command
}

func printMessages(output io.Writer, messages []model.Message) error {
	if len(messages) == 0 {
		_, err := fmt.Fprintln(output, "No messages")
		return err
	}
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		sender := message.ContactName
		if sender == "" {
			sender = message.SenderAddress
		}
		if sender == "" {
			sender = "Unknown sender"
		}
		timestamp := "Unknown time"
		if !message.Timestamp.IsZero() {
			timestamp = message.Timestamp.Local().Format("2006-01-02 15:04")
		}
		if _, err := fmt.Fprintf(
			output,
			"%s  %s  %s\n",
			timestamp,
			textsafe.OneLine(sender),
			textsafe.OneLine(message.Body),
		); err != nil {
			return err
		}
	}
	return nil
}

func printContacts(output io.Writer, contacts []model.Contact) error {
	if len(contacts) == 0 {
		_, err := fmt.Fprintln(output, "No contacts")
		return err
	}
	for _, contact := range contacts {
		name := textsafe.OneLine(contact.Name)
		if name == "" {
			name = "Unnamed contact"
		}
		addresses := append(append([]string(nil), contact.Phones...), contact.Emails...)
		if len(addresses) == 0 {
			if _, err := fmt.Fprintln(output, name); err != nil {
				return err
			}
			continue
		}
		for _, address := range addresses {
			if _, err := fmt.Fprintf(output, "%s  %s\n", name, textsafe.OneLine(address)); err != nil {
				return err
			}
		}
	}
	return nil
}

func connectionWord(connected bool) string {
	if connected {
		return "connected"
	}
	return "disconnected"
}

package discord

import (
	"encoding/json"
	"sort"

	"github.com/bwmarrin/discordgo"
)

// commandsEquivalent reports whether the guild's currently registered commands
// already match the desired set, so AGX can skip the rate-limited bulk overwrite
// on connect. It compares only the fields AGX controls (name, description, type,
// and options), normalizing Discord-supplied defaults.
func commandsEquivalent(existing, desired []*discordgo.ApplicationCommand) bool {
	a, errA := commandSetSignature(existing)
	b, errB := commandSetSignature(desired)
	return errA == nil && errB == nil && a == b
}

type commandSig struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Type        int         `json:"type"`
	Options     []optionSig `json:"options,omitempty"`
}

type optionSig struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Type        int         `json:"type"`
	Required    bool        `json:"required"`
	Choices     []choiceSig `json:"choices,omitempty"`
	Options     []optionSig `json:"options,omitempty"`
}

type choiceSig struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

func commandSetSignature(cmds []*discordgo.ApplicationCommand) (string, error) {
	sigs := make([]commandSig, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd == nil {
			continue
		}
		sigs = append(sigs, normalizeCommand(cmd))
	}
	// Discord may return commands in a different order than AGX defines them.
	sort.Slice(sigs, func(i, j int) bool { return sigs[i].Name < sigs[j].Name })
	data, err := json.Marshal(sigs)
	return string(data), err
}

func normalizeCommand(cmd *discordgo.ApplicationCommand) commandSig {
	commandType := int(cmd.Type)
	if commandType == 0 {
		// Discord returns 1 (chat input) for the default AGX leaves unset.
		commandType = int(discordgo.ChatApplicationCommand)
	}
	return commandSig{
		Name:        cmd.Name,
		Description: cmd.Description,
		Type:        commandType,
		Options:     normalizeOptions(cmd.Options),
	}
}

func normalizeOptions(opts []*discordgo.ApplicationCommandOption) []optionSig {
	if len(opts) == 0 {
		return nil
	}
	out := make([]optionSig, 0, len(opts))
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		out = append(out, optionSig{
			Name:        opt.Name,
			Description: opt.Description,
			Type:        int(opt.Type),
			Required:    opt.Required,
			Choices:     normalizeChoices(opt.Choices),
			Options:     normalizeOptions(opt.Options),
		})
	}
	return out
}

func normalizeChoices(choices []*discordgo.ApplicationCommandOptionChoice) []choiceSig {
	if len(choices) == 0 {
		return nil
	}
	out := make([]choiceSig, 0, len(choices))
	for _, choice := range choices {
		if choice == nil {
			continue
		}
		out = append(out, choiceSig{Name: choice.Name, Value: choice.Value})
	}
	return out
}

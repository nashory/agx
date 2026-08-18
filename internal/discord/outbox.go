package discord

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nashory/agx/internal/db"
)

const discordOutboxBatchSize = 100

var discordOutboxRetryDelay = time.Second

type idempotentMessageSender interface {
	SendMessageOnce(ctx context.Context, channelID, nonce, content string) error
}

type idempotentInteractivePromptSender interface {
	SendInteractivePromptOnce(ctx context.Context, channelID, nonce string, prompt InteractivePrompt) error
}

func (b *Bridge) QueueRenderActions(ctx context.Context, taskID, channelID, eventKey string, actions []RenderAction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	store := b.store
	b.mu.Unlock()
	if store == nil {
		return fmt.Errorf("discord delivery store is not configured")
	}
	deliveries := make([]db.DiscordDelivery, 0, len(actions))
	for index, action := range actions {
		if action.Kind != RenderSend || strings.TrimSpace(action.Content) == "" {
			continue
		}
		kind := db.DiscordDeliveryMessage
		var promptJSON *string
		if action.Prompt != nil {
			encoded, err := json.Marshal(action.Prompt)
			if err != nil {
				return err
			}
			value := string(encoded)
			promptJSON = &value
			kind = db.DiscordDeliveryInteractive
		}
		deliveries = append(deliveries, db.DiscordDelivery{
			DeliveryKey: discordDeliveryKey(taskID, channelID, eventKey, index, action),
			TaskID:      taskID,
			ChannelID:   channelID,
			Kind:        kind,
			Content:     action.Content,
			PromptJSON:  promptJSON,
			EventKey:    strings.TrimSpace(eventKey),
		})
	}
	if err := store.EnqueueDiscordDeliveries(deliveries); err != nil {
		return err
	}
	b.wakeDiscordDelivery()
	return nil
}

func discordDeliveryKey(taskID, channelID, eventKey string, index int, action RenderAction) string {
	prompt := ""
	if action.Prompt != nil {
		if encoded, err := json.Marshal(action.Prompt); err == nil {
			prompt = string(encoded)
		}
	}
	value := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s\x00%s", taskID, channelID, eventKey, index, action.Content, prompt)
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (b *Bridge) wakeDiscordDelivery() {
	select {
	case b.delivery <- struct{}{}:
	default:
	}
}

func (b *Bridge) runDiscordDeliveryLoop(ctx context.Context, store *db.Store, sender MessageSender) {
	for {
		delivered, err := deliverDiscordOutboxOnce(ctx, store, sender)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			b.setError(err)
			timer := time.NewTimer(discordOutboxRetryDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-b.delivery:
				timer.Stop()
			case <-timer.C:
			}
			continue
		}
		if delivered > 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-b.delivery:
		}
	}
}

func deliverDiscordOutboxOnce(ctx context.Context, store *db.Store, sender MessageSender) (int, error) {
	deliveries, err := store.ListPendingDiscordDeliveries(discordOutboxBatchSize)
	if err != nil {
		return 0, err
	}
	delivered := 0
	blockedChannels := map[string]struct{}{}
	var firstErr error
	for _, delivery := range deliveries {
		if _, blocked := blockedChannels[delivery.ChannelID]; blocked {
			continue
		}
		if err := ctx.Err(); err != nil {
			return delivered, err
		}
		err := retrySemanticSend(ctx, func() error {
			return sendDiscordDelivery(ctx, sender, delivery)
		})
		if err != nil {
			_ = store.MarkDiscordDeliveryFailed(delivery.DeliveryKey, err)
			blockedChannels[delivery.ChannelID] = struct{}{}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := store.MarkDiscordDeliveryDelivered(delivery.DeliveryKey); err != nil {
			return delivered, err
		}
		delivered++
	}
	return delivered, firstErr
}

func sendDiscordDelivery(ctx context.Context, sender MessageSender, delivery db.DiscordDelivery) error {
	nonce := discordDeliveryNonce(delivery.DeliveryKey)
	if delivery.Kind == db.DiscordDeliveryInteractive && delivery.PromptJSON != nil {
		var prompt InteractivePrompt
		if err := json.Unmarshal([]byte(*delivery.PromptJSON), &prompt); err != nil {
			return err
		}
		if interactive, ok := sender.(idempotentInteractivePromptSender); ok {
			return interactive.SendInteractivePromptOnce(ctx, delivery.ChannelID, nonce, prompt)
		}
		if interactive, ok := sender.(InteractivePromptSender); ok {
			return interactive.SendInteractivePrompt(ctx, delivery.ChannelID, prompt)
		}
	}
	if idempotent, ok := sender.(idempotentMessageSender); ok {
		return idempotent.SendMessageOnce(ctx, delivery.ChannelID, nonce, delivery.Content)
	}
	return sender.SendMessage(ctx, delivery.ChannelID, delivery.Content)
}

func discordDeliveryNonce(deliveryKey string) string {
	deliveryKey = strings.TrimSpace(deliveryKey)
	if len(deliveryKey) > 25 {
		return deliveryKey[:25]
	}
	return deliveryKey
}

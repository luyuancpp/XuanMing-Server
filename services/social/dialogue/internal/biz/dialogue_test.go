package biz

import (
	"context"
	"testing"
	"time"

	"github.com/luyuancpp/pandora/pkg/errcode"

	"github.com/luyuancpp/pandora/services/social/dialogue/internal/data"
)

const testNpcID = 1001

// newTestTree 构造一棵覆盖「跳转 / 终止 / 隐藏选项 / 结束」各分支的对话树。
func newTestTree() map[uint32]*data.DialogueTree {
	return map[uint32]*data.DialogueTree{
		testNpcID: {
			NpcID:     testNpcID,
			Speaker:   "商店老板",
			StartNode: "greet",
			Nodes: map[string]*data.DialogueNode{
				"greet": {
					NodeID: "greet",
					Text:   "你好,冒险者。",
					Options: []data.DialogueOption{
						{OptionID: "menu", Text: "看看商品", Visible: true, NextNode: "menu"},
						{OptionID: "bye", Text: "再见", Visible: true, NextNode: ""},
						{OptionID: "secret", Text: "暗号", Visible: false, NextNode: "secret"},
					},
				},
				"menu": {
					NodeID: "menu",
					Text:   "想买点什么?",
					Options: []data.DialogueOption{
						{OptionID: "info", Text: "了解详情", Visible: true, NextNode: "info"},
						{OptionID: "close", Text: "关闭", Visible: true, NextNode: ""},
					},
				},
				"info":   {NodeID: "info", Text: "这是一把好剑。"}, // 终止节点(无选项)
				"secret": {NodeID: "secret", Text: "隐藏分支。"},
			},
		},
	}
}

func newUsecase() *DialogueUsecase {
	return NewDialogueUsecase(
		data.NewConfigTreeProvider(newTestTree()),
		data.NewMemorySessionStore(),
		time.Minute,
	)
}

func TestStartDialogue_Success(t *testing.T) {
	u := newUsecase()
	st, err := u.StartDialogue(context.Background(), 7, testNpcID, 1000)
	if err != nil {
		t.Fatalf("StartDialogue err: %v", err)
	}
	if st.GetDialogueId() != 1000 || st.GetNpcId() != testNpcID || st.GetNodeId() != "greet" {
		t.Fatalf("unexpected state: %+v", st)
	}
	if st.GetSpeaker() != "商店老板" {
		t.Fatalf("speaker = %q", st.GetSpeaker())
	}
	// greet 有 2 个可见选项(secret 不可见被过滤)。
	if len(st.GetOptions()) != 2 {
		t.Fatalf("visible options = %d, want 2", len(st.GetOptions()))
	}
	if st.GetEnded() {
		t.Fatalf("greet should not be ended")
	}
}

func TestStartDialogue_UnknownNpc(t *testing.T) {
	u := newUsecase()
	_, err := u.StartDialogue(context.Background(), 7, 9999, 1000)
	if got := errcode.As(err); got != errcode.ErrDialogueNotFound {
		t.Fatalf("code = %d, want ErrDialogueNotFound", got)
	}
}

func TestStartDialogue_InvalidArgs(t *testing.T) {
	u := newUsecase()
	cases := []struct {
		name     string
		player   uint64
		npc      uint32
		dialogue uint64
	}{
		{"no player", 0, testNpcID, 1000},
		{"no npc", 7, 0, 1000},
		{"no dialogue_id", 7, testNpcID, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := u.StartDialogue(context.Background(), c.player, c.npc, c.dialogue)
			if got := errcode.As(err); got != errcode.ErrInvalidArg {
				t.Fatalf("code = %d, want ErrInvalidArg", got)
			}
		})
	}
}

func TestChooseOption_AdvanceNode(t *testing.T) {
	u := newUsecase()
	ctx := context.Background()
	if _, err := u.StartDialogue(ctx, 7, testNpcID, 1000); err != nil {
		t.Fatal(err)
	}
	st, err := u.ChooseOption(ctx, 7, 1000, "menu")
	if err != nil {
		t.Fatalf("ChooseOption err: %v", err)
	}
	if st.GetNodeId() != "menu" || st.GetEnded() {
		t.Fatalf("expected node menu not ended, got %+v", st)
	}
	if len(st.GetOptions()) != 2 {
		t.Fatalf("menu options = %d, want 2", len(st.GetOptions()))
	}
}

func TestChooseOption_TerminalNodeEnds(t *testing.T) {
	u := newUsecase()
	ctx := context.Background()
	_, _ = u.StartDialogue(ctx, 7, testNpcID, 1000)
	_, _ = u.ChooseOption(ctx, 7, 1000, "menu")
	st, err := u.ChooseOption(ctx, 7, 1000, "info")
	if err != nil {
		t.Fatalf("ChooseOption err: %v", err)
	}
	if !st.GetEnded() || st.GetNodeId() != "info" {
		t.Fatalf("expected ended info node, got %+v", st)
	}
	// 会话应已回收:再选择返回 not found。
	_, err = u.ChooseOption(ctx, 7, 1000, "info")
	if got := errcode.As(err); got != errcode.ErrDialogueNotFound {
		t.Fatalf("after end code = %d, want ErrDialogueNotFound", got)
	}
}

func TestChooseOption_EmptyNextEnds(t *testing.T) {
	u := newUsecase()
	ctx := context.Background()
	_, _ = u.StartDialogue(ctx, 7, testNpcID, 1000)
	st, err := u.ChooseOption(ctx, 7, 1000, "bye") // NextNode == ""
	if err != nil {
		t.Fatalf("ChooseOption err: %v", err)
	}
	if !st.GetEnded() {
		t.Fatalf("bye should end dialogue, got %+v", st)
	}
}

func TestChooseOption_InvalidOption(t *testing.T) {
	u := newUsecase()
	ctx := context.Background()
	_, _ = u.StartDialogue(ctx, 7, testNpcID, 1000)
	_, err := u.ChooseOption(ctx, 7, 1000, "nope")
	if got := errcode.As(err); got != errcode.ErrDialogueOptionInvalid {
		t.Fatalf("code = %d, want ErrDialogueOptionInvalid", got)
	}
}

func TestChooseOption_InvisibleOptionRejected(t *testing.T) {
	u := newUsecase()
	ctx := context.Background()
	_, _ = u.StartDialogue(ctx, 7, testNpcID, 1000)
	// secret 在配置里 visible=false,即使客户端回传 option_id 也必须拒绝。
	_, err := u.ChooseOption(ctx, 7, 1000, "secret")
	if got := errcode.As(err); got != errcode.ErrDialogueOptionInvalid {
		t.Fatalf("code = %d, want ErrDialogueOptionInvalid", got)
	}
}

func TestChooseOption_OtherPlayerSessionHidden(t *testing.T) {
	u := newUsecase()
	ctx := context.Background()
	_, _ = u.StartDialogue(ctx, 7, testNpcID, 1000)
	// 玩家 8 拿玩家 7 的 dialogue_id → 按不存在处理(不泄露他人会话)。
	_, err := u.ChooseOption(ctx, 8, 1000, "menu")
	if got := errcode.As(err); got != errcode.ErrDialogueNotFound {
		t.Fatalf("code = %d, want ErrDialogueNotFound", got)
	}
}

func TestEndDialogue_Idempotent(t *testing.T) {
	u := newUsecase()
	ctx := context.Background()
	_, _ = u.StartDialogue(ctx, 7, testNpcID, 1000)
	if err := u.EndDialogue(ctx, 7, 1000); err != nil {
		t.Fatalf("EndDialogue err: %v", err)
	}
	// 再次结束不报错(幂等)。
	if err := u.EndDialogue(ctx, 7, 1000); err != nil {
		t.Fatalf("EndDialogue second err: %v", err)
	}
	// 结束后无法继续选择。
	_, err := u.ChooseOption(ctx, 7, 1000, "menu")
	if got := errcode.As(err); got != errcode.ErrDialogueNotFound {
		t.Fatalf("code = %d, want ErrDialogueNotFound", got)
	}
}

func TestSession_Expiry(t *testing.T) {
	u := NewDialogueUsecase(
		data.NewConfigTreeProvider(newTestTree()),
		data.NewMemorySessionStore(),
		time.Nanosecond, // 立即过期
	)
	ctx := context.Background()
	if _, err := u.StartDialogue(ctx, 7, testNpcID, 1000); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	_, err := u.ChooseOption(ctx, 7, 1000, "menu")
	if got := errcode.As(err); got != errcode.ErrDialogueNotFound {
		t.Fatalf("expired session code = %d, want ErrDialogueNotFound", got)
	}
}

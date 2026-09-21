package main

import (
	"bytes"
	"testing"
)

func TestListBuddyFitsBesideListItems(t *testing.T) {
	style := agentPageStyle("projects")
	icon := listBuddyAnimation(listBuddyQingzhu, "projects")[0]
	itemRight := style.X + style.BorderWidth + style.PaddingLength + style.ListItemWidth
	cardRight := style.X + style.Width - style.BorderWidth - style.PaddingLength
	if icon.X < itemRight || icon.X+icon.Width > cardRight {
		t.Fatalf("list buddy x=%d..%d does not fit rail %d..%d", icon.X, icon.X+icon.Width, itemRight, cardRight)
	}
	if icon.Y < style.Y || icon.Y+icon.Height > style.Y+style.Height {
		t.Fatalf("list buddy y=%d..%d is outside list card", icon.Y, icon.Y+icon.Height)
	}
}

func TestPetListBuddyAnimationsLoadThreeFrames(t *testing.T) {
	for _, mode := range []string{listBuddyQingzhu, listBuddyXiaojiaqi} {
		frames := listBuddyAnimation(mode, "projects")
		if len(frames) != 3 {
			t.Fatalf("%s frames = %d, want 3", mode, len(frames))
		}
		if bytes.Equal(frames[0].BMP, frames[1].BMP) || bytes.Equal(frames[1].BMP, frames[2].BMP) {
			t.Fatalf("%s animation contains duplicate adjacent frames", mode)
		}
	}
}

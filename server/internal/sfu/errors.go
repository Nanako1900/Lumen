package sfu

import "errors"

// ErrRoomNotFound is returned when signalling targets a channel with no room.
var ErrRoomNotFound = errors.New("语音房间不存在")

#!/usr/bin/env python3
"""Minimal X11 input for checking the desktop build under Xvfb (no xdotool
in the environment): XTest through ctypes.

    xinput.py click X Y        left click at screen coordinates
    xinput.py rclick X Y       right click (a context menu)
    xinput.py key NAME...      press keys (X keysym names: l, Return, Escape;
                               modifiers: ctrl+a, shift+Tab)
    xinput.py type TEXT        type ASCII text
    xinput.py drag X1 Y1 X2 Y2 press at (X1,Y1), move in steps, release at (X2,Y2)
    xinput.py group N          lock keyboard layout group N (0 = first; with
                               `setxkbmap -layout us,ru` 1 is Russian: keys keep
                               their codes, the text is Cyrillic)
    xinput.py close WINDOW     ask a window (id, 0x…) to close: WM_DELETE_WINDOW,
                               like a window manager's close button
    xinput.py resize WINDOW W H resize a window (no window manager under Xvfb)
    xinput.py scroll X Y N     wheel at (X,Y): N notches down (negative: up)
    xinput.py sleep SECONDS

Several commands can be chained: xinput.py click 300 200 key Return sleep 1
Uses $DISPLAY. Test tooling only.
"""
import ctypes
import sys
import time

x11 = ctypes.cdll.LoadLibrary("libX11.so.6")
xtst = ctypes.cdll.LoadLibrary("libXtst.so.6")
x11.XOpenDisplay.restype = ctypes.c_void_p
x11.XOpenDisplay.argtypes = [ctypes.c_char_p]
x11.XStringToKeysym.restype = ctypes.c_ulong
x11.XStringToKeysym.argtypes = [ctypes.c_char_p]
x11.XKeysymToKeycode.restype = ctypes.c_ubyte
x11.XKeysymToKeycode.argtypes = [ctypes.c_void_p, ctypes.c_ulong]
x11.XFlush.argtypes = [ctypes.c_void_p]
xtst.XTestFakeMotionEvent.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.c_int, ctypes.c_int, ctypes.c_ulong]
xtst.XTestFakeButtonEvent.argtypes = [ctypes.c_void_p, ctypes.c_uint, ctypes.c_int, ctypes.c_ulong]
xtst.XTestFakeKeyEvent.argtypes = [ctypes.c_void_p, ctypes.c_uint, ctypes.c_int, ctypes.c_ulong]

dpy = x11.XOpenDisplay(None)
if not dpy:
    sys.exit("cannot open display")


class XClientMessageEvent(ctypes.Structure):
    _fields_ = [
        ("type", ctypes.c_int),
        ("serial", ctypes.c_ulong),
        ("send_event", ctypes.c_int),
        ("display", ctypes.c_void_p),
        ("window", ctypes.c_ulong),
        ("message_type", ctypes.c_ulong),
        ("format", ctypes.c_int),
        ("data", ctypes.c_long * 5),
    ]


class XEvent(ctypes.Union):
    _fields_ = [("xclient", XClientMessageEvent), ("pad", ctypes.c_long * 24)]


x11.XInternAtom.restype = ctypes.c_ulong
x11.XInternAtom.argtypes = [ctypes.c_void_p, ctypes.c_char_p, ctypes.c_int]
x11.XSendEvent.argtypes = [ctypes.c_void_p, ctypes.c_ulong, ctypes.c_int, ctypes.c_long, ctypes.POINTER(XEvent)]
x11.XkbLockGroup.argtypes = [ctypes.c_void_p, ctypes.c_uint, ctypes.c_uint]


def flush():
    x11.XFlush(dpy)
    time.sleep(0.05)


def click(x, y, button=1):
    xtst.XTestFakeMotionEvent(dpy, -1, x, y, 0)
    flush()
    xtst.XTestFakeButtonEvent(dpy, button, 1, 0)
    flush()
    xtst.XTestFakeButtonEvent(dpy, button, 0, 0)
    flush()


def drag(x1, y1, x2, y2, steps=10):
    xtst.XTestFakeMotionEvent(dpy, -1, x1, y1, 0)
    flush()
    xtst.XTestFakeButtonEvent(dpy, 1, 1, 0)
    flush()
    for i in range(1, steps + 1):
        xtst.XTestFakeMotionEvent(dpy, -1, x1 + (x2 - x1) * i // steps, y1 + (y2 - y1) * i // steps, 0)
        flush()
    xtst.XTestFakeButtonEvent(dpy, 1, 0, 0)
    flush()


def close(window):
    ev = XEvent()
    ev.xclient.type = 33  # ClientMessage
    ev.xclient.window = window
    ev.xclient.message_type = x11.XInternAtom(dpy, b"WM_PROTOCOLS", 0)
    ev.xclient.format = 32
    ev.xclient.data[0] = x11.XInternAtom(dpy, b"WM_DELETE_WINDOW", 0)
    x11.XSendEvent(dpy, window, 0, 0, ctypes.byref(ev))
    flush()


MODS = {"ctrl": "Control_L", "shift": "Shift_L", "alt": "Alt_L"}


def keycode(name):
    name = {"ArrowDown": "Down", "ArrowUp": "Up", "ArrowLeft": "Left", "ArrowRight": "Right"}.get(name, name)
    symbol = x11.XStringToKeysym(name.encode())
    if not symbol:
        sys.exit(f"unknown key {name}")
    code = x11.XKeysymToKeycode(dpy, symbol)
    if not code:
        sys.exit(f"unmapped key {name}")
    return code


def key(spec, shift=False):
    *mods, name = spec.split("+") if len(spec) > 1 else [spec]
    mods = [MODS[m] for m in mods] + (["Shift_L"] if shift else [])
    for m in mods:
        xtst.XTestFakeKeyEvent(dpy, keycode(m), 1, 0)
    code = keycode(name)
    xtst.XTestFakeKeyEvent(dpy, code, 1, 0)
    xtst.XTestFakeKeyEvent(dpy, code, 0, 0)
    for m in reversed(mods):
        xtst.XTestFakeKeyEvent(dpy, keycode(m), 0, 0)
    flush()


NAMES = {" ": "space", "-": "minus", ".": "period", "/": "slash", "[": "bracketleft", "]": "bracketright",
         "=": "equal", ";": "semicolon", ",": "comma", "'": "apostrophe", "\\": "backslash"}
SHIFTED = {"*": "8", "(": "9", ")": "0", "|": "backslash", "+": "equal", "?": "slash", "^": "6", "$": "4",
           '"': "apostrophe", "&": "7", ">": "period", "<": "comma", "_": "minus", ":": "semicolon", "!": "1"}

args = sys.argv[1:]
while args:
    cmd = args.pop(0)
    if cmd == "click":
        click(int(args.pop(0)), int(args.pop(0)))
    elif cmd == "rclick":
        click(int(args.pop(0)), int(args.pop(0)), button=3)
    elif cmd == "key":
        while args and args[0] not in ("click", "rclick", "key", "type", "sleep", "drag", "group", "close", "resize", "scroll"):
            key(args.pop(0))
    elif cmd == "type":
        for ch in args.pop(0):
            if ch in SHIFTED:
                key(SHIFTED[ch], shift=True)
            else:
                key(NAMES.get(ch, ch), shift=ch.isupper())
    elif cmd == "drag":
        drag(*(int(args.pop(0)) for _ in range(4)))
    elif cmd == "group":
        x11.XkbLockGroup(dpy, 0x100, int(args.pop(0)))  # XkbUseCoreKbd
        flush()
    elif cmd == "close":
        close(int(args.pop(0), 0))
    elif cmd == "resize":
        x11.XResizeWindow(ctypes.c_void_p(dpy), ctypes.c_ulong(int(args.pop(0), 0)), ctypes.c_uint(int(args.pop(0))), ctypes.c_uint(int(args.pop(0))))
        flush()
    elif cmd == "scroll":
        x, y, n = (int(args.pop(0)) for _ in range(3))
        xtst.XTestFakeMotionEvent(dpy, -1, x, y, 0)
        for _ in range(abs(n)):
            b = 5 if n > 0 else 4
            xtst.XTestFakeButtonEvent(dpy, b, 1, 0)
            xtst.XTestFakeButtonEvent(dpy, b, 0, 0)
            flush()
    elif cmd == "sleep":
        time.sleep(float(args.pop(0)))
    else:
        sys.exit(f"unknown command {cmd}")

#!/usr/bin/env python3
"""Minimal X11 input for checking the desktop build under Xvfb (no xdotool
in the environment): XTest through ctypes.

    xinput.py click X Y        left click at screen coordinates
    xinput.py key NAME...      press keys (X keysym names: l, Return, Escape;
                               modifiers: ctrl+a, shift+Tab)
    xinput.py type TEXT        type ASCII text
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


def flush():
    x11.XFlush(dpy)
    time.sleep(0.05)


def click(x, y):
    xtst.XTestFakeMotionEvent(dpy, -1, x, y, 0)
    flush()
    xtst.XTestFakeButtonEvent(dpy, 1, 1, 0)
    flush()
    xtst.XTestFakeButtonEvent(dpy, 1, 0, 0)
    flush()


MODS = {"ctrl": "Control_L", "shift": "Shift_L", "alt": "Alt_L"}


def keycode(name):
    code = x11.XKeysymToKeycode(dpy, x11.XStringToKeysym(name.encode()))
    if not code:
        sys.exit(f"unknown key {name}")
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


NAMES = {" ": "space", "-": "minus", ".": "period", "/": "slash", "[": "bracketleft", "]": "bracketright"}
SHIFTED = {"*": "8", "(": "9", ")": "0", "|": "backslash", "+": "equal", "?": "slash", "^": "6", "$": "4"}

args = sys.argv[1:]
while args:
    cmd = args.pop(0)
    if cmd == "click":
        click(int(args.pop(0)), int(args.pop(0)))
    elif cmd == "key":
        while args and args[0] not in ("click", "key", "type", "sleep"):
            key(args.pop(0))
    elif cmd == "type":
        for ch in args.pop(0):
            if ch in SHIFTED:
                key(SHIFTED[ch], shift=True)
            else:
                key(NAMES.get(ch, ch), shift=ch.isupper())
    elif cmd == "sleep":
        time.sleep(float(args.pop(0)))
    else:
        sys.exit(f"unknown command {cmd}")

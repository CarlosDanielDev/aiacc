"""Drive the compiled TestAnimateHandsOffRealTTY binary (TUI_PTY_TEST=1) through
a pty as the foreground session leader, so /dev/tty reads get input without
SIGTTIN.

The keypress sequence mirrors the owner's hands: 'e' once READY, then ONE
blind \\r at +1.5s — no handshake, no retry. A retry would mask the very bug
this pins: in the broken world the outgoing reader eats the first \\r and its
next read fails on the expired deadline, so a retry would land as keyEnter on
the (already working) next screen and report GREEN with or without the fix.

Writes the verdict on stdout; exit 0 only on GREEN. Kills and reaps the child
on every exit path.

Usage: python3 tools/tui-pty-driver.py <compiled-tui.test>"""
import os, pty, select, sys, time

if len(sys.argv) < 2:
    print("usage: tui-pty-driver.py <compiled-tui.test>")
    sys.exit(2)
testbin = sys.argv[1]

pid, fd = pty.fork()
if pid == 0:
    os.chdir("/tmp")
    os.environ["TERM"] = "xterm-256color"
    os.environ["TUI_PTY_TEST"] = "1"
    os.execvp(testbin, [testbin, "-test.run", "TestAnimateHandsOffRealTTY",
                        "-test.v", "-test.count=1", "-test.timeout", "60s"])

buf = bytearray()
t0 = time.time()
sent_e = sent_cr = False
deadline = t0 + 20
verdict = "TIMEOUT"
child_done = False
while time.time() < deadline:
    r, _, _ = select.select([fd], [], [], 0.3)
    if r:
        try:
            chunk = os.read(fd, 65536)
        except OSError:
            break
        if not chunk:
            break
        buf.extend(chunk)
    txt = buf.decode(errors="replace")
    if not sent_e and "READY" in txt:
        sent_e = True
        os.write(fd, b"e")
    if sent_e and not sent_cr and time.time() - t0 > 1.5:
        # One blind follow-up, exactly the owner's hands. The fix's join
        # finishes ~100ms after 'e', so in the fixed world this \r simply
        # arrives on the next screen; in the broken world it is consumed.
        sent_cr = True
        os.write(fd, b"\r")
    if "PHASE2-OK" in txt and "--- PASS" in txt:
        verdict = "GREEN"
        break
    if "--- FAIL" in txt:
        verdict = "RED"
        break
    if "--- SKIP" in txt or "no controlling terminal" in txt:
        verdict = "SKIP"
        break
    if "PASS" in txt and sent_cr and sent_e:
        # test binary exited cleanly without PHASE2-OK: the follow-up was lost
        verdict = "RED"
        break

time.sleep(0.3)
try:
    os.kill(pid, 9)
except OSError:
    pass
try:
    _, status = os.waitpid(pid, 0)
    child_done = True
except ChildProcessError:
    child_done = True
except OSError:
    pass
try:
    os.close(fd)
except OSError:
    pass
txt = buf.decode(errors="replace")
tail = "\n".join(txt.splitlines()[-25:])
print(tail)
print(f"\n=== VERDICT: {verdict} (e:{sent_e} cr:{sent_cr} reaped:{child_done}) ===")
sys.exit(0 if verdict == "GREEN" else 1)

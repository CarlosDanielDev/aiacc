"""Drive the compiled TestAnimateHandsOffRealTTY binary (TUI_PTY_TEST=1) through
a pty as the foreground session leader, so /dev/tty reads get input without
SIGTTIN. Writes 'e' after READY, PHASE1-OK triggers the follow-up \r (blind,
like the owner), retries once — retrying compensates for pty writes lost
upstream of aiacc under machine load; if aiacc still ate keys, retries would
vanish too, so the verdict stays meaningful.

Usage: python3 tools/tui-pty-driver.py <compiled-tui.test>"""
import os, pty, sys, time

testbin = sys.argv[1]
pid, fd = pty.fork()
if pid == 0:
    os.chdir("/tmp")
    os.environ["TERM"] = "xterm-256color"
    os.environ["TUI_PTY_TEST"] = "1"
    os.execvp(testbin, [testbin, "-test.run", "TestAnimateHandsOffRealTTY",
                        "-test.v", "-test.count=1", "-test.timeout", "60s"])

buf = bytearray()
sent_e = sent_follow = False
retry_budget = False
deadline = time.time() + 45
verdict = "TIMEOUT"
while time.time() < deadline:
    try:
        chunk = os.read(fd, 65536)
    except OSError:
        break
    if not chunk:
        break
    buf.extend(chunk)
    if not sent_e and b"READY" in buf:
        sent_e = True
        time.sleep(0.3)
        os.write(fd, b"e")
    if not sent_follow and sent_e and time.time() > deadline - 43.5:
        # The owner types blind: 1.5s after 'e', no handshake. (PHASE1-OK only
        # prints AFTER the join completes, which is exactly the bug.)
        sent_follow = True
        n1 = os.write(fd, b"\r")
        buf.extend(f"[DRV wrote CR n={n1}]".encode())
    if sent_follow and time.time() > deadline - 42.0 and (not retry_budget):
        retry_budget = True
        time.sleep(1.0)
        try:
            n2 = os.write(fd, b"\r")
            n3 = os.write(fd, b"Z")
            buf.extend(f"[DRV retry CR n={n2} Z n={n3}]".encode())
        except OSError as e:
            buf.extend(f"[DRV retry failed: child exited]".encode())
            break
    text = buf.decode(errors="replace")
    if "PHASE2-OK" in text and "--- PASS" in text:
        verdict = "GREEN"
        break
    if "--- FAIL" in text:
        verdict = "RED"
        break
    if "--- SKIP" in text or "no controlling terminal" in text:
        verdict = "SKIP"
        break

time.sleep(0.5)
try:
    os.close(fd)
except OSError:
    pass
text = buf.decode(errors="replace")
tail = "\n".join(text.splitlines()[-25:])
print(tail)
print(f"\n=== VERDICT: {verdict} (e:{sent_e} follow:{sent_follow}) ===")
sys.exit(0 if verdict == "GREEN" else 1)

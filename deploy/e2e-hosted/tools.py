# Sidecar of the `make e2e-hosted` stack (project shop-e2e-hosted). It runs INSIDE the hosted api
# container's network namespace (network_mode: service:api) and mounts the data volume read-only:
#   - it forwards 0.0.0.0:18501 to the admin listener on 127.0.0.1:8081, so the admin API (which only
#     accepts loopback peers, unchanged) is reachable from the test through one published port;
#   - the test runs `python3 -c ...` in it (compose exec) to look at files under /data.
# Throwaway: lives and dies with the e2e project.
import asyncio

LISTEN = ("0.0.0.0", 18501)
TARGET = ("127.0.0.1", 8081)


async def pipe(r, w):
    try:
        while data := await r.read(65536):
            w.write(data)
            await w.drain()
    except Exception:
        pass
    finally:
        try:
            w.close()
        except Exception:
            pass


async def handle(cr, cw):
    try:
        sr, sw = await asyncio.open_connection(*TARGET)
    except Exception:
        cw.close()
        return
    await asyncio.gather(pipe(cr, sw), pipe(sr, cw))


async def main():
    srv = await asyncio.start_server(handle, *LISTEN)
    async with srv:
        await srv.serve_forever()


asyncio.run(main())

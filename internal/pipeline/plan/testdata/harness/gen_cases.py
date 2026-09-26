import json, re

J1 = {"format": "jpeg", "width": 1000, "height": 600}
J2 = {"format": "jpeg", "width": 600, "height": 1000}
JODD = {"format": "jpeg", "width": 1001, "height": 601}
JBIG = {"format": "jpeg", "width": 4000, "height": 3000}
P1 = {"format": "png", "width": 1000, "height": 600, "alpha": True}
PS = {"format": "png", "width": 200, "height": 100}
W1 = {"format": "webp", "width": 1600, "height": 900}
G1 = {"format": "gif", "width": 200, "height": 150, "frames": 5}
SV = {"format": "svg", "width": 300, "height": 200}
TR = {"format": "png", "width": 400, "height": 300, "inner": [50, 40, 200, 100]}


def jo(o, w=1000, h=600):
    return {"format": "jpeg", "width": w, "height": h, "orientation": o}


cases = [
    # fit
    (J1, "rs:fit:300:200"), (J1, "rs:fit:300:0"), (J1, "rs:fit:0:200"), (J1, "rs:fit:300:300"),
    (J1, "rs:fit:2000:2000"), (J1, "rs:fit:2000:2000:1"), (J1, "rs:fit:2000:0:1"), (JODD, "rs:fit:333:0"),
    (JODD, "rs:fit:100:100"),
    # fill
    (J1, "rs:fill:300:300"), (J1, "rs:fill:300:300/g:nowe"), (J1, "rs:fill:300:300/g:so:10:20"),
    (J1, "rs:fill:300:300/g:ea:0.1:0"), (J1, "rs:fill:300:300/g:fp:0.8:0.3"), (J1, "rs:fill:300:300/g:sm"),
    (J1, "rs:fill:2000:1000"), (J1, "rs:fill:2000:1000:1"), (J2, "rs:fill:300:300/g:soea:5:5"),
    (JODD, "rs:fill:250:250/g:we:-10:3"),
    # fill-down
    (J1, "rs:fill-down:2000:500"), (J1, "rs:fill-down:500:2000"), (J1, "rs:fill-down:300:200"),
    (J1, "rs:fill-down:2000:500:1"),
    # force
    (J1, "rs:force:300:300"), (J1, "rs:force:300:0"), (J1, "rs:force:2000:100"), (J1, "rs:force:2000:100:1"),
    # auto
    (J1, "rs:auto:300:200"), (J1, "rs:auto:200:300"), (J2, "rs:auto:300:200"),
    # extend
    (J1, "rs:fit:1200:1200:0:1"), (J1, "rs:fit:1200:1200:0:1:so"), (J1, "rs:fit:300:300:0:1"),
    (J1, "rs:fit:1200:800/ex:1:nowe:10:10"), (J1, "rs:fill:300:300/ex:1:ea"),
    # extend aspect ratio
    (J1, "rs:fit:1200:1200/exar:1"), (J1, "rs:fill-down:2000:500/exar:1:ea"), (J1, "rs:fit:300:300/exar:1:no"),
    # padding
    (J1, "rs:fit:300:200/pd:10:20:30:40"), (J1, "pd:10/dpr:2/rs:fit:300:200"), (J1, "pd:15"),
    # dpr
    (J1, "rs:fit:300:200/dpr:2"), (PS, "rs:fit:300:200/dpr:3"), (J1, "rs:fill:100:100/dpr:2/g:nowe:10:10"),
    (PS, "rs:fit:300:200/dpr:2/ex:1"), (PS, "rs:fit:300:200/dpr:2/ex:1:noea:5:5"), (J1, "rs:fit:300:200/dpr:0.5"),
    (PS, "rs:fit:300:200:1/dpr:2"),
    # zoom
    (J1, "z:0.5"), (J1, "z:2:1/rs:fit:300:200"), (J1, "z:1.5/el:1"), (J1, "z:0.5/dpr:2/rs:fill:200:200"),
    # min width / height
    (J1, "rs:fit:300:200/mw:500"), (J1, "rs:fit:300:200/mh:500"), (J1, "rs:fill:100:100/mw:200"),
    (J1, "rs:fit:300:0/mh:400"),
    # crop
    (J1, "c:500:300"), (J1, "c:500:300:nowe"), (J1, "c:0.5:0.5"), (J1, "c:0.5:0.5:so/rs:fit:200:200"),
    (J1, "c:300:300:fp:0.2:0.8/rs:fill:100:100"), (J1, "c:2000:100"), (J1, "c:400:300:sm"),
    (J1, "c:400:300:soea:20:30"), (J1, "c:0.25:0/g:ea"), (J1, "c:500:500/rs:fill:200:100/g:so"),
    # rotate / flip
    (J1, "rot:90/rs:fill:300:200"), (J1, "rot:180/c:300:200:nowe"), (J1, "rot:270/rs:fit:300:0"),
    (J1, "fl:1:0/c:300:200:we"), (J1, "fl:0:1/rs:fill:200:300/g:no"), (J1, "rot:90/fl:1:1/c:300:200:nowe:10:20"),
    (J1, "rot:90/c:0.5:0.25:fp:0.1:0.9"),
    # EXIF orientation
    (jo(6), "rs:fill:300:200"), (jo(6), "rs:fit:300:0"), (jo(6), "c:300:200:nowe"), (jo(6), "ar:0/rs:fit:300:0"),
    (jo(3), "rs:fill:300:200/g:nowe"), (jo(5), "c:300:200:noea"), (jo(8), "rs:fill:200:300/rot:90"),
    (jo(2), "c:200:100:we:10:0"), (jo(7), "c:300:200:fp:0.3:0.6/rs:fit:100:100"), (jo(4), "c:300:200:so:0:15"),
    # shrink on load
    (JBIG, "rs:fit:400:0"), (JBIG, "rs:fit:1000:0"), (JBIG, "rs:fill:300:300"), (JBIG, "rs:fit:3000:0"),
    (JBIG, "c:1000:1000:nowe:100:100/rs:fit:200:200"), (jo(6, 4000, 3000), "rs:fit:500:0"),
    (JBIG, "rs:fit:1999:0"), (JBIG, "rs:force:500:3000"),
    # webp scale on load
    (W1, "rs:fit:400:0/f:png"), (W1, "rs:fill:500:500/f:jpg"), (W1, "rs:fit:333:0/f:png"),
    # max result dimension
    (J1, "mrd:500/rs:fit:2000:2000:1"), (J1, "mrd:300"), (J1, "mrd:500/rs:fit:600:600/pd:50"),
    (J1, "mrd:400/rs:fit:1000:1000:0:1"), (J1, "mrd:400/rs:fill:900:900:1/exar:1"),
    # max source resolution
    (J1, "msr:1/rs:fit:100:100"), (J1, "msr:0.5/rs:fit:100:100"),
    # animated
    (G1, "rs:fit:100:100"), (G1, "rs:fill:100:100/g:nowe"), (G1, "c:100:100/rs:fit:50:50"), (G1, "maf:1/rs:fit:100:100"),
    (G1, "maf:3/rs:fit:100:100"), (G1, "pd:5/rs:fit:100:100"), (G1, "rs:fill:120:120/ex:1/g:sm"),
    (G1, "mafr:0.05/rs:fit:100:100"), (G1, "mafr:0.01/rs:fit:100:100"), (G1, "rs:fit:100:100/f:png"),
    (G1, "t:10/rs:fit:100:100"), (G1, "rot:90/rs:fit:100:0/dpr:2"),
    # svg
    (SV, "rs:fit:600:0/f:png"), (SV, "rs:fit:100:0/f:png"), (SV, "msr:0.01/rs:fit:100:0/f:png"), (SV, "msr:0.01/f:png"),
    # trim
    (TR, "t:10"), (TR, "t:10/rs:fit:100:100"), (TR, "t:10:FFFFFF:1:1"), (TR, "t:10/c:100:50:nowe/rs:fill:50:50"),
    # filters, background, alpha
    (J1, "bl:2/rs:fit:300:200"), (J1, "pix:5"), (J1, "sh:1.5/rs:fit:300:200"),
    (P1, "bg:255:0:0/rs:fit:300:200/f:png"), (P1, "rs:fill:300:300/f:png"), (P1, "rs:fill:300:300/f:jpg"),
]

out = []
seen = set()
for src, url in cases:
    tag = src["format"] + str(src["width"]) + "x" + str(src["height"])
    if src.get("orientation"):
        tag += "_o" + str(src["orientation"])
    if src.get("alpha"):
        tag += "_alpha"
    if src.get("frames"):
        tag += "_f" + str(src["frames"])
    if src.get("inner"):
        tag += "_trim"
    name = tag + "/" + url
    assert name not in seen, name
    seen.add(name)
    out.append({"name": name, "src": src, "url": url})
print(len(out))
json.dump(out, open("cases.json", "w"), indent=1)

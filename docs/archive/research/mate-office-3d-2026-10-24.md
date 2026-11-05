# Mate Office 3D — khảo sát kỹ thuật, asset và chi phí — 2026-10-24

Nghiên cứu chỉ đọc: tài liệu chính thức, mã nguồn, metadata package và file trên máy captain. Không cài npm, không sửa file nào ngoài ghi chú này. Có một thí nghiệm nhỏ: chạy Blender 5.2.2 headless render một khung EEVEE và xuất GLB vào `/tmp` để xác nhận lệnh. Mọi con số byte đo bằng `wc -c` / `gzip -9c` trên file thật; con số ngày công là ước lượng của người viết và ghi rõ là ước lượng.

## 1. Câu hỏi và kết luận ngắn

Captain muốn Mate Office thành cảnh 3D màu sắc, kiểu game / render Blender, trước hết cho marketing (landing, TikTok, ảnh chụp), sau đó mới là dashboard sống.

**Đề xuất chính — một bộ asset, hai đầu ra, marketing đi trước:**

1. Dựng **một bộ asset glTF** chung: mascot harness đã có sẵn trong `threejsvideo` (4 GLB), nội thất CC0 của Kenney (bàn, ghế xoay, màn hình, bàn phím, laptop, tủ sách, cây), tường/sàn tự dựng trong Blender theo palette landing (paper `#f5f3eb`, coral). Blender 5.2.2 LTS trên máy render EEVEE headless và xuất GLB (có Draco) được — đã chạy thử, 7,4 s cho một khung 320×240 (mục 4).
2. **Marketing trước, không đụng binary** (phương án 4): Blender/EEVEE render ảnh hero + sprite, `threejsvideo` render clip MP4 từ chính bộ GLB đó (pipeline `preview → build --profile final` đã có, chạy Chrome GPU + FFmpeg). Đây là đường ngắn nhất tới ảnh/video đẹp, không bị ràng buộc §10.
3. **Office sống 3D sau** (phương án 1): three.js vendored vào binary, ghim **0.180.x vì đó là bản cuối còn ship `three.module.min.js` + `three.core.min.js`** (0.186 đã bỏ bản min — mục 6), `MeshToonMaterial` + `OutlineEffect`, camera `OrthographicCamera` góc isometric, render-on-demand gắn vào long-poll. Tăng binary ~1,5–2 MB trên 29,2 MB. Giữ trang DOM hiện tại làm bản mặc định/fallback, 3D là route riêng, vì §10 cấm CDN/build step và test node hiện tại không lái được canvas.

**Fallback:** phương án 2 — sprite 2.5D render sẵn từ Blender (sheet PNG/WebP theo state và hướng), ghép vào trang DOM/SVG hiện tại. Không WebGL, test node hiện tại vẫn chạy, nặng thêm vài trăm KB ảnh; đổi lại không xoay camera, thêm state/harness mới là render lại.

Loại: Spline (export code/GLTF có màu phải trả Pro, runtime 36 MB, licence runtime không ghi), PlayCanvas/Babylon (bundle 2,5–8,7 MB, không cần tính năng gì three.js thiếu), Godot web (wasm + header COOP/COEP, không hợp "một file HTML trong binary").

Điểm rủi ro lớn nhất không phải kỹ thuật mà là **logo harness**: Anthropic cấm biến đổi trademark và chỉ cho dùng trong tài liệu được duyệt trước; OpenAI chưa đọc được trang brand (403); pi chỉ có MIT cho code, logo không ghi. Mục 5.

### 1b. Prototype cùng ngày (nhánh `proto/office-3d`)

Để captain chọn look bằng mắt thay vì bằng bảng, có một prototype vứt đi ở `internal/dashboard/proto-office-3d/` trên nhánh `proto/office-3d` (không nằm trong binary; three.js 0.186.1 và GLB mascot chép từ `threejsvideo` bằng `fetch-vendor.sh`, phục vụ bằng `python3 -m http.server 4180`). Ba look trên cùng một trang và cùng dữ liệu tĩnh (3 phòng, crew `working`/`stuck`/`waiting_review`/`walking`, Mate `asleep`), đổi bằng `?variant=A|B|C`; ảnh chụp 1600×900 ở `shots/`:

| Biến thể | Cách dựng | Nhận xét khi chụp |
| --- | --- | --- |
| A · Diorama toon | `OrthographicCamera` isometric, `MeshToonMaterial` gradient 3 bậc, `OutlineEffect` 0.0032, phòng là slab nổi có 2 tường cắt, palette kẹo trên nền kem | "Game" nhất; viền đen làm mọi khối đọc rõ ở cỡ nhỏ; các phòng xếp chéo nên nhãn phòng sau chồng lên tường phòng trước, cần giãn theo z |
| B · Voxel playground | `PerspectiveCamera` 38° từ trên, sàn ô vuông, thảm thay tường, mọi vật là `BoxGeometry`, `MeshLambertMaterial` + bóng đổ, crew nhún squash kiểu Crossy Road | Hợp palette landing (paper + coral) nhất, dễ cắt thành clip dọc; thiếu chiều sâu "phòng" nên ít giống office |
| C · Clay studio | `RoundedBoxGeometry` + `MeshStandardMaterial` roughness 0,62, `PCFSoftShadowMap`, ACES, bệ tròn, camera quay chậm 26° | Giống still Blender nhất, sang; ít "game", mascot tối dưới ACES cần chỉnh emissive/exposure |

Cả ba đều dùng GLB mascot không mặt làm đầu; GLB phải ép `metalness = 0` mới sáng dưới đèn (material gốc tối). Prototype chỉ là mood test: không đọc `/api`, không drawer, không test.

## 2. Hiện trạng

**Mate Office hôm nay.** `internal/dashboard/ui/office/{index.html,office.js,office.css}`: trang DOM + SVG, 2 189 dòng JS, dựng node từng cái với `textContent`, icon là path SVG 24×24 (`IC` trong `office.js`). Nhúng vào binary bằng `//go:embed ui` trong `internal/dashboard/ui.go`, phục vụ tại `/office/`. Kích thước: `internal/dashboard/ui` 492K, trong đó `ui/office` 292K (office.js 111 868 B, office.css 56 238 B, fonts 120K). Binary đã cài `/opt/homebrew/bin/mate` 29 178 914 B.

**Luật §10/§11 (`docs/dashboard.md`).** Cùng origin, GET only, không build step, không framework, không CDN. `TestUIMakesNoOffOriginRequest` (`internal/dashboard/ui_assets_test.go`) quét mọi file nhúng: `<script src=`, `<link href=`, `<img src=`, `fetch(`, `url(`, `@import`, `new WebSocket/EventSource/Worker` trỏ ra ngoài origin đều fail, và **mọi chuỗi `http://`/`https://` ngoài comment cũng fail**, chỉ trừ `http://www.w3.org/2000/svg` được thay rỗng trước khi quét. Font vendored dưới `ui/office/fonts`, OFL để ở `internal/dashboard/fontlicenses/` vì văn bản licence có URL. Test hành vi `ui_office_test.cjs` chạy `office.js` trong một DOM giả viết tay trong node; "Layout, colour and the drawings are checked in a real browser (docs/dashboard.md section 11), not here" — bằng chứng trình duyệt làm qua Playwright như `docs/archive/evidence/m6-dashboard-2026-09-21.md`.

**Mô hình dữ liệu.** Một phòng mỗi project, Mate ở bàn lớn, một bàn mỗi crew mở, tủ hồ sơ task đã đóng, hành lang có bàn captain + whiteboard (§11). State crew `docs/timeline.md` §9.1: `arriving`, `at_desk_working`, `walking_to_ceo`, `waiting_at_ceo`, `waiting_review`, `leaving`, `gone`, plus pause `asleep`/`blocked`; Mate: `idle`, `on_phone`, `receiving_digest`, `reading`, `deciding`, `answering`, `reviewing`, `merging`. `walking_to_ceo` và `leaving` là tức thời (edge ghi cả hai state cùng `at`) — renderer tự animate bước đi. Harness là "mặt": Claude / Codex / Pi.

**Quyết định đã chốt của captain (coi là cho trước).** Mascot = icon app harness thật thành khối squircle 3D không mặt, cảm xúc qua tư thế; palette landing paper `#f5f3eb` + coral + serif (`/Users/erics/app/mate-landing/dist/styles.css` dòng 2: `--paper:#f5f3eb`, `--coral:#ba5236`, `--serif:'DM Serif Display'` — lưu ý coral trong CSS là `#ba5236`, không phải `#e77859`); nền charcoal tối đã bị chê.

**Asset có sẵn trên máy.**

| Nơi | Có gì | Tái dụng |
| --- | --- | --- |
| `/Users/erics/app/threejsvideo` | three `0.186.1` (không phải 0.180), `puppeteer-core`; pipeline `new/preview/check/stills/render/encode/verify/build`, Chrome GPU (`--use-angle=metal`), FFmpeg. Mascot GLB `references/export/<id>/<id>.glb`: claude 20K (116 tam giác, 3 mesh), pi 124K, grok 188K, codex 372K (nhúng artwork làm texture). Mỗi GLB có 5 clip lặp Idle/Bounce/Wiggle/Greet/Spin; front +Z, up +Y, đáy Y=0; mặt chính unlit. Không phải git repo. | Dùng thẳng làm mascot trong cả Office lẫn video; "không thêm bệ, tay chân" đúng ý captain. Codex nặng vì texture, nên ép WebP/giảm size khi nhúng. |
| `/Users/erics/app/3dviz-pro-max` | Skill MIT, three.js 0.180 theo README; 22 kit blueprint: `buildings/`, `props/` (barrel, cart, crate, fence, lantern, signboard, well, clothesline), `nature/`, `creatures/`, `gltf/` (4 GLB hero Blender 140K–1,9 MB), `layout/`; rigs `lighting-studio.js`, `post-stack.js`, `camera-orbit-follow.js`; 37 style profile gồm `style-cel-shaded`, `style-toy-miniature`, `style-tilt-shift-diorama`, `style-low-poly`, `style-papercraft`, `style-clay`; `scripts/capture.py` (Playwright Chromium, `--gpu` dùng ANGLE Metal trên macOS, đo chất lượng khung, mã exit theo lỗi console); `templates/kits/blender/export-village-assets.py` là mẫu bpy headless → GLB; `references/blender-threejs-handoff.md`. **Không có kit nội thất/văn phòng/bàn ghế** (catalog có direction `architecture-interiors` 18 recipe nhưng kit là làng/nhà thôn). Cài: copy/symlink `skills/3dviz-pro-max` vào `~/.claude/skills/` hoặc `claude --plugin-dir`; templates phải copy ra ngoài rồi `pnpm install`. | Dùng profile toon/diorama làm số liệu đèn, tone mapping, viền; dùng `capture.py` làm bộ chụp bằng chứng; kit props không khớp văn phòng. |
| `/Users/erics/app/mate-landing/dist` | Landing buildless; hero là PNG tĩnh `assets/hero-harnesses-3d.png` 1 923 059 B, 1536×1024. | Thay bằng ảnh render Blender/three cùng kích thước là xong, không đổi cấu trúc. |
| Blender | `/Applications/Blender.app` = **Blender 5.2.2 LTS** (build 2026-09-15). `-E help` liệt kê `BLENDER_EEVEE`, `BLENDER_WORKBENCH`, `CYCLES`. glTF add-on báo Draco và MeshOptimizer bridge có sẵn. | Render + export headless đã chạy thử (mục 4). |
| node | v24.21.0; `gltf-transform` chưa cài. | |
| Skill Claude Code | `topview-3d-builder:topview-3d-cli` (`~/.claude/plugins/marketplaces/topview-3d-builder/skills/topview-3d-cli/SKILL.md`): block-out cảnh bằng khối + 4 mannequin, render PNG, mục đích là reference cho video gen. `muse2api-video` (`~/.claude/skills/muse2api-video/SKILL.md`): text/image→video qua gateway local `127.0.0.1:18610`. | Chỉ cho đường video marketing (storyboard → clip AI); không phải đường render Office. Chưa gọi. |

## 3. Các phương án

| # | Phương án | Look đạt được | Chi phí runtime | Công (ước lượng) | Giấy phép | Hợp §10? | Giá trị marketing |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | three.js WebGL trong Office | Toon + viền, low-poly, isometric, có thể thêm DOF giả tilt-shift; xoay/zoom được | +~1,5–2 MB binary; GPU; render-on-demand thì gần 0 CPU khi im | 8–15 ngày gồm test/bằng chứng | MIT | Có, nếu vendored + import map nội bộ + sửa 1 ngoại lệ test (mục 6) | Cao: cảnh sống, quay màn hình thật |
| 2 | Sprite 2.5D từ Blender vào trang DOM | Đúng look Blender, cố định góc | +vài trăm KB ảnh; không GPU | 5–8 ngày | Blender GPL không dính output | Có, không sửa test | Trung bình: tĩnh, nhưng vẫn là ảnh render đẹp |
| 3a | Spline | Đẹp nhanh, có editor | runtime npm 36,2 MB unpacked; export code/GLTF màu phải Pro | thấp để thử | Free có watermark web; licence runtime không ghi trong registry | Không (runtime ngoài, CDN) | Thấp cho Office; được cho ảnh/clip tay |
| 3b | PlayCanvas | Engine đủ | `playcanvas.min.js` 2 554 078 B | — | MIT | Có thể vendored, nhưng nặng gấp 3,5 three min | — |
| 3c | Babylon.js | Engine đủ | `babylon.js` UMD 8 745 228 B | — | Apache-2.0 | Quá nặng cho một trang nhúng binary | — |
| 3d | Godot web | Game thật | wasm lớn (gzip còn ~1/4), thread cần header COOP/COEP | — | MIT | Không: cần header/service worker, pck + wasm | Cao cho video, nhưng là một dự án riêng |
| 4 | Chỉ marketing: Blender/EEVEE + threejsvideo | Tối đa (Blender) | 0 | 3–6 ngày cho hero + 1 clip | Blender output tự do; asset theo nguồn | Không liên quan binary | Cao nhất trên mỗi ngày công |

Ghi chú theo nguồn:

- three.js: `MeshToonMaterial` "A material implementing toon shading", `gradientMap` phải `NearestFilter`, `NoColorSpace` [1]. `OutlineEffect` "An outline effect for toon shaders. Note that this class can only be used with WebGLRenderer" [2]. `OutlinePass` "A pass for rendering outlines around selected objects" qua `EffectComposer` [3] — dùng để highlight crew được chọn, không phải viền toàn cảnh. `OrthographicCamera`: "an object's size in the rendered image stays constant regardless of its distance from the camera" [4] — đúng cho isometric. `GLTFLoader` hỗ trợ `KHR_draco_mesh_compression`, `KHR_materials_unlit`, `KHR_texture_basisu`, `EXT_meshopt_compression`, `EXT_texture_webp` [5]. Ví dụ chính thức: `webgl_materials_toon`, `webgl_postprocessing_outline`, `webgl_loader_gltf`, `webgl_camera` [6].
- Render-on-demand: manual "if the user is on portable device it wastes the user's battery. The most obvious way to solve this is to render once at the start and then render only when something changes" với `requestRenderIfNotRequested()` [7]. `WebGLRenderer.setAnimationLoop`: "Applications are advised to always define the animation loop with this method and not manually with requestAnimationFrame() for best compatibility" [8] — dùng cho lúc có animation bước đi, rồi `setAnimationLoop(null)` khi cảnh đứng yên.
- Spline: trang giá ghi Free "Web exports with watermark"; Hobby "No watermark on web exports"; Pro mới có "Download Code Export", "Export GLTF with color & texture", "Export Video"; Enterprise "Self-Hosted Export" [9]. `@splinetool/runtime` 2.0.111, `unpackedSize` 36 235 422 B, không có trường `license` trong registry [10].
- PlayCanvas `playcanvas` 2.23.3 MIT [11], `build/playcanvas.min.js` 2 554 078 B [12]. Babylon `@babylonjs/core` 9.32.0 Apache-2.0 [13], `babylonjs/babylon.js` 8 745 228 B [14]. Godot MIT [15]; web export: thread cần `Cross-Origin-Opener-Policy: same-origin` + `Cross-Origin-Embedder-Policy: require-corp`, từ 4.3 mặc định single-thread, wasm gzip "down to around a quarter of its original size" [16]; kích thước tuyệt đối: chưa xác minh.

## 4. Pipeline asset đề xuất

**Nguồn sự thật:** file `.blend` (hoặc script bpy như 3dviz) → GLB → `gltf-transform` → (a) three.js trong Office, (b) threejsvideo cho clip, (c) EEVEE render ảnh/sprite. Một bộ asset, ba đầu ra.

### 4.1 Blender → GLB (đã chạy thử trên máy)

Manual: `-b, --background` "Run in background (often used for UI-less rendering)"; `-E, --engine <engine>`; `-P, --python <filepath>`; `--factory-startup`; "Arguments are executed in the order they are given!" — "Always position -f or -a as the last arguments" [17][18]. Ví dụ manual: `blender -b file.blend -o /project/renders/frame_##### -F OPEN_EXR -f -2`, `blender -b file.blend -E CYCLES -s 10 -e 500 -t 2 -a` [17].

Thí nghiệm 2026-10-24 (file `/tmp/mate-research/blender/render_test.py`, không trong repo):

```sh
/Applications/Blender.app/Contents/MacOS/Blender -b --factory-startup --python-exit-code 1 --python render_test.py
# trong script: scene.render.engine='BLENDER_EEVEE'; bpy.ops.render.render(write_still=True)
#               bpy.ops.export_scene.gltf(filepath='cube.glb', export_format='GLB',
#                   export_apply=True, export_yup=True, export_draco_mesh_compression_enable=False)
```

Kết quả: `Saved: cube.png` (320×240, 46 493 B), `cube.glb` 1 936 B, log "Draco is available", "MeshOptimizer is available", tổng 7,4 s wall. Tức EEVEE headless trên macOS này chạy, không cần mở UI. Chữ ký API `bpy.ops.export_scene.gltf(...)` có `export_format`, `export_draco_mesh_compression_enable`, `export_yup`, `export_apply` ("Apply modifiers (excluding Armatures)... prevents exporting shape keys"), `export_animations` [19]. Add-on glTF: "Materials (Principled BSDF) and Shadeless (Unlit)", "quads and n-gons are automatically converted to triangles", ảnh phải PNG/JPEG, "To export an unlit material, mix in a camera ray, and avoid using the Principled BSDF node" [20]. EEVEE: "realtime render engine focused on speed and interactivity", "based on rasterization and is not a path tracer" [21].

Mẫu script sẵn có để chép: `3dviz-pro-max/skills/3dviz-pro-max/templates/kits/blender/export-village-assets.py` — build mesh bằng `bmesh`, một slot Principled mỗi màu, `EXPORT_KWARGS = dict(export_format='GLB', export_apply=True, export_yup=True, export_extras=True, ...)`, ghi socket vào `extras` để three.js đọc `userData` [22].

### 4.2 gltf-transform (tùy chọn, cho Office)

`@gltf-transform/cli` 4.5.1, MIT, bin `gltf-transform` [23]. Lệnh theo docs [24]:

```sh
npm install --global @gltf-transform/cli
gltf-transform inspect office.glb
gltf-transform optimize office.glb office.opt.glb --compress draco --texture-compress webp
```

Với bộ low-poly màu phẳng thì bỏ `--texture-compress` (không texture) và cân nhắc **không Draco**: decoder tốn 250 KB (mục 6) trong khi geometry phẳng đã nhỏ; chỉ dùng Draco nếu tổng GLB vượt ~1 MB. Không cần KTX2 (basis transcoder 585 KB).

### 4.3 three.js trong Office (phương án 1)

- Vendored `ui/office/vendor/three.module.min.js` + `three.core.min.js` (0.180.x; bản min import `./three.core.min.js` tương đối — đã đọc header file) và các addon cần: `GLTFLoader.js` (117 570 B), `OutlineEffect.js` (12 661 B), tùy chọn `DRACOLoader.js` (19 030 B) + `libs/draco/`, `OrbitControls.js` (40 755 B).
- Addon import `'three'` bằng bare specifier (`from 'three'` ở `OutlineEffect.js:7`, `OutlinePass.js:15`, `GLTFLoader.js:67`, `DRACOLoader.js:13`) [25]; manual: "Addons do not need to be installed separately, but do need to be imported separately" và cần import map [26]. Import map **nội bộ**, không CDN:

```html
<script type="importmap">
{ "imports": { "three": "./vendor/three.module.min.js", "three/addons/": "./vendor/addons/" } }
</script>
<script type="module" src="office3d.js"></script>
```

- `DRACOLoader` mặc định tìm decoder bằng `new URL('../libs/draco/draco_decoder.wasm', import.meta.url)` [27] → giữ cây thư mục `addons/libs/draco/` thì không cần `setDecoderPath`.
- Vòng render: `renderer.render()` khi `/api/events` trả về hoặc drawer đổi; `setAnimationLoop(fn)` chỉ trong lúc có bước đi, xong `setAnimationLoop(null)`.
- Look: `MeshToonMaterial` với `gradientMap` 3 bậc, `OutlineEffect` viền `0.004–0.006`, `OrthographicCamera` nghiêng ~35° / xoay 45°, đèn một key cứng + hemisphere; số liệu khởi đầu lấy từ `style-cel-shaded.json` / `style-tilt-shift-diorama.json` của 3dviz (neutral tone mapping, rim 0.45× key, hull 1,5 px) [28].

### 4.4 Sprite 2.5D (phương án 2)

Blender: một camera ortho cố định, render từng mascot × state (idle, working, walking 8 khung, waiting, blocked, asleep) × 2–4 hướng, EEVEE `-F PNG` với nền trong suốt, ghép sheet bằng script; đồ nội thất render một lần theo phòng. Trang DOM hiện tại chỉ thay `<svg>` icon bằng `<img>`/`background-image` cùng origin. Dữ liệu và drawer giữ nguyên.

### 4.5 Video marketing (phương án 4)

`threejsvideo`: `npm run new -- mate-office`, `npm run preview`, `npm run build -- mate-office --profile final` (720×1280/30 fps; đổi 1080×1920 trong `project.json`) — pipeline doctor → check → stills → render → encode → verify, GPU Chrome + FFmpeg [29]. Scene import cùng GLB và helper `player/model-animations.js`. Đường khác: `topview-3d-cli` block-out → `muse2api-video` image-to-video — chỉ khi cần cảnh "AI" không dựng tay.

## 5. Nguồn asset và giấy phép

| Nguồn | URL | Giấy phép (nguyên văn tại nguồn) | Có gì | Định dạng |
| --- | --- | --- | --- | --- |
| Kenney Furniture Kit | kenney.nl/assets/furniture-kit [30] | "Creative Commons CC0"; FAQ: "all game assets on the asset pages are public domain licensed (CC0)... Attribution is not required... Do not use our logo" [31] | Trang gốc: 140 models. Bản mirror poly.pizza liệt kê 116 model gồm Desk, Desk Corner, Desk Chair, Computer Screen, Computer Keyboard, Laptop, Bookcase, Potted Plant, Coffee Machine, Lamp, Rug, Doorway, Wall Window [32] | kenney.nl không ghi định dạng trên trang; mirror poly.pizza cho "Download FBX / Download GLTF" [32] |
| Kenney Mini Characters | kenney.nl/assets/mini-characters [33] | "Creative Commons CC0" | 25 file, có nhãn Animation | chưa thấy trên trang |
| Kenney City Kit (Commercial) | kenney.nl/assets/city-kit-commercial [34] | "Creative Commons CC0" | 50 file nhà cao tầng | chưa thấy trên trang |
| Quaternius Ultimate Furniture | quaternius.com/packs/ultimatefurniture.html [35] | Trang pack ghi "CC0"; **nhưng** trang licence hiện là "Quaternius Asset License (QAL) v1.0, Last updated: 8/28/2026": "No attribution is required", được bán Product, cấm "Resell or redistribute the Assets themselves"; §7 "Changes will not apply retroactively to Assets you've already obtained" [36] | 20 model giường/ghế/bàn | FBX, OBJ, Blend — **không ghi glTF** |
| poly.pizza | poly.pizza/bundle/Furniture-Kit-NoG1sEUD1z [32] | Mỗi model/bundle ghi licence riêng ("Licence : Public Domain (CC0)" cho bundle Kenney); không có trang `/about`, `/license`, `/faq` (404) | Mirror Kenney/Quaternius + model lẻ | FBX, GLTF |
| Sketchfab | api.sketchfab.com/v3/licenses [37] | API: `cc0` "CC0 Public Domain — Credit is not mandatory. Commercial use is allowed."; `by` "Author must be credited"; `by-nc` "No commercial use" | Lọc Downloadable + licence khi tìm | tải glTF/USDZ... |
| Mascot harness | `threejsvideo/references/export/*.glb` | Model do captain dựng theo ảnh gốc của logo; **quyền với logo thuộc chủ nhãn** (dưới) | 4 GLB có animation | GLB |

**Logo harness làm mascot 3D — tình trạng pháp lý:**

- Anthropic, *Trademark Guidelines* [38]: "You may only use our trademarks as specifically permitted by us and only in materials we approve beforehand." "No alterations of our trademarks (changes to color, font, proportion, or otherwise) are permitted." "You may not use our trademarks in a manner that implies Anthropic's sponsorship or endorsement, or a relationship or affiliation with Anthropic, except as we expressly authorize." Liên hệ: marketing@anthropic.com. → Khối 3D đùn dày, animate Bounce/Greet là "alteration"; dùng trong marketing là ngoài phạm vi tự động. Brandfolder công khai của Anthropic có ghi "Please comply with all usage guidelines" [39].
- OpenAI, openai.com/brand: **không đọc được (HTTP 403 cho fetch tự động)** — chưa xác minh. Kết quả tìm kiếm (thứ cấp, không dùng làm căn cứ) nói "use the logo exactly as provided", "don't modify".
- pi: repo `badlogic/pi-mono` MIT (LICENSE "Copyright (c) 2025 Mario Zechner") [40]; README nhúng logo từ `https://pi.dev/logo-auto.svg`; **không thấy tuyên bố trademark/licence cho logo** — chưa xác minh.
- Kenney: "Do not use our logo" [31] — không liên quan mascot nhưng nhắc khi credit.

Hệ quả: landing hiện tại (`hero-harnesses-3d.png`) đã dùng cả bốn biểu tượng; chuyển sang 3D không thay đổi bản chất rủi ro nhưng tăng độ "biến đổi". Hai cách giảm: (a) xin phép (Anthropic có địa chỉ rõ), (b) mascot "gợi" bằng squircle + màu + chữ cái, không tái tạo mark chính xác — là quyết định của captain, ghi chú này không tự chọn.

## 6. Chi phí

**Kích thước (đo thật).**

| Thành phần | raw | gzip -9 | Ghi chú |
| --- | --- | --- | --- |
| three 0.180.0 `three.module.min.js` | 338 908 | 78 814 | import `./three.core.min.js` |
| three 0.180.0 `three.core.min.js` | 381 124 | 100 485 | chứa chuỗi `http://www.w3.org/1999/xhtml` ngoài comment |
| **Cộng three min** | **720 032** | **179 299** | |
| three 0.180.0 không min (`module`+`core`) | 2 006 568 | 394 547 | nếu không dùng bản min |
| three 0.186.1 (threejsvideo) `module`+`core` | 2 120 885 | 417 318 | **0.186 không còn file min** |
| GLTFLoader + OutlineEffect | 130 231 | 29 100 | |
| DRACOLoader + `libs/draco/gltf/` (wasm + wrapper) | 269 906 | 80 690 | tùy chọn |
| OrbitControls | 40 755 | 8 490 | tùy chọn |
| KTX2 basis transcoder (wasm + js) | 584 862 | 259 658 | không cần |
| Mascot GLB ×4 (threejsvideo) | ~704K | — | codex 372K vì texture |
| GLB nội thất low-poly | ước ~200–600 KB | — | **ước lượng**; so sánh: GLB Blender của 3dviz 140K–1,9 MB, cube test 1,9 KB |

Ước tính tăng binary cho phương án 1: three min 0,72 MB + addon 0,13 MB + GLB ~0,5–1,3 MB (+0,27 MB nếu Draco) ≈ **1,4–2,4 MB** trên 29,2 MB hiện tại (≈ +5–8%). `embed.FS` phục vụ file không nén; gzip trên wire là việc của server nếu muốn.

**Bản min của three và luật "no build step".** `three` 0.180.0–0.185.0 ship `three.module.min.js` + `three.core.min.js` trong `build/` (metadata unpkg) [41]; 0.186.0 và 0.186.1 **không còn file `*.min.js`** [41][42] — không tìm thấy dòng release note/migration guide nào nói về việc bỏ (Migration Guide chỉ ghi r160→r161 bỏ `build/three.js` và `build/three.min.js` [43]). Muốn vendored một cặp file min mà không chạy bundler thì ghim 0.180.x–0.185.x; không thì vendored bản không min (2 MB raw, 0,4 MB gz) hoặc tự minify (= build step, trái §10). Manual cũng cảnh báo: "Import all dependencies from the same version of three.js" [26].

**Hai điểm va luật §10 cần quyết trước khi code:**

1. `three.core.min.js` chứa chuỗi `http://www.w3.org/1999/xhtml` (namespace `createElementNS`) ngoài comment → `TestUIMakesNoOffOriginRequest` fail; cần thêm ngoại lệ namespace như đã làm cho SVG (`ui_assets_test.go`), không phải nới luật.
2. `<script type="importmap">` + `type="module"` là hai thứ trang hiện tại chưa dùng; test asset chỉ bắt `src=` off-origin nên import map tương đối qua được. Nhưng `docs/dashboard.md` §10 nói "loaded as classic scripts" — cần sửa câu đó khi merge.

**Hiệu năng dashboard long-poll.** Office poll `/api/events` `wait=20` (5 khi mở drawer). Render-on-demand: vẽ một khung khi dữ liệu đổi hoặc người dùng kéo camera; animation bước đi chạy `setAnimationLoop` trong ≤1–2 s rồi tắt. Theo manual, render liên tục "wastes the user's battery" [7]. Với 3–5 phòng, ≤20 nhân vật low-poly, một khung ortho ở 1080p là việc nhẹ cho GPU tích hợp (ước lượng, chưa đo).

**Test và bằng chứng.** Canvas WebGL không có `textContent` nên `ui_office_test.cjs` không lái được cảnh. Hướng: (a) giữ **mọi dữ liệu và drawer trong DOM** như nay, 3D chỉ là lớp vẽ đọc một `scene model` JS thuần — test node vẫn kiểm `scene model` (ai đứng đâu, state nào) không cần WebGL; (b) ảnh chụp bằng Chromium headless có GPU: `3dviz/scripts/capture.py --gpu` dùng `--use-angle=metal` trên macOS và ghi tên renderer thật vào `capture-log.json` ("a wrong flag shows up as capture_mode 'swiftshader'") [44]; `threejsvideo/scripts/lib/browser.mjs` dùng cùng cờ cho puppeteer [45]; (c) bằng chứng ảnh vào `docs/evidence/` như `m6-dashboard-2026-09-21.md` (Playwright). Trong CI không GPU thì SwiftShader vẽ được nhưng chậm; chỉ nên chụp để so hash, không assert pixel.

**Accessibility.** §10 đòi "colour is never the only difference" và chữ qua `textContent`: giữ nhãn/tooltip/drawer DOM, canvas gắn `aria-hidden`, state vẫn có chữ + glyph trong DOM. 3D là trang trí có dữ liệu, không phải nguồn dữ liệu duy nhất.

**Công (ước lượng của người viết, chưa có số đo):**

| Việc | Ngày |
| --- | --- |
| Bộ asset Blender: 1 phòng, bàn lớn, 3 bàn crew, tủ hồ sơ, hành lang, whiteboard, palette paper/coral; export GLB; 6 pose/state cho mascot | 3–4 |
| Ảnh hero + 1 clip TikTok 20–30 s qua threejsvideo | 1–2 |
| Office 3D route riêng: vendored three, import map, loader, toon+outline, scene model từ `/api`, render-on-demand, walk animation, drawer click bằng raycast | 5–8 |
| Test scene model + chụp bằng chứng GPU + sửa test asset + docs §10/§11 | 2–3 |
| Sprite 2.5D (fallback) thay cho hai dòng trên | 4–6 |

**Rủi ro:** logo harness (mục 5); ghim three ở 0.180 nghĩa là không nhận sửa lỗi mới; WebGL bị tắt trên vài máy/VM (cần fallback về trang DOM — có sẵn); Quaternius đổi licence sang QAL (vẫn dùng được nhưng không còn là CC0 với pack mới); GLB codex 372K vì texture nhúng.

## 7. Việc còn mở / chưa xác minh

- OpenAI brand guidelines: chưa đọc được trang gốc (403). Pi: licence logo chưa thấy. Anthropic: đã có nguyên văn nhưng chưa hỏi marketing@anthropic.com.
- Kích thước thật của GLB Kenney (chưa tải zip, kenney.nl không ghi định dạng; glTF chỉ xác nhận qua mirror poly.pizza).
- Quaternius pack "CC0" trên trang pack vs QAL trên trang licence: chưa rõ pack Ultimate Furniture tải hôm nay rơi vào bản nào.
- Godot web export: kích thước tuyệt đối; Spline: licence `@splinetool/runtime`.
- Vì sao three bỏ `*.min.js` ở 0.186: chỉ thấy qua nội dung package, không thấy release note.
- Hiệu năng thực tế của toon+outline trên Intel/Apple GPU với 20 nhân vật: chưa đo. SwiftShader trong CI: chưa thử.
- `MeshToonMaterial` với `OutlineEffect` cần `WebGLRenderer`, không WebGPU [2] — nếu sau này muốn WebGPU thì là `ToonOutlinePassNode`, chưa xem.
- Chưa kiểm tra three 0.180 có lỗi/deprecation nào với `OutlineEffect` + `OrthographicCamera` (đọc từ 0.186 trên máy; 0.180 chỉ đo kích thước).
- 3dviz "harness-village" demo là tư liệu lịch sử, không phải bằng chứng skill vẽ văn phòng (README tự ghi).

## 8. Nguồn

1. `/Users/erics/app/threejsvideo/node_modules/three/src/materials/MeshToonMaterial.js` (three 0.186.1); docs: https://threejs.org/docs/pages/MeshToonMaterial.html
2. `.../three/examples/jsm/effects/OutlineEffect.js`; https://threejs.org/docs/pages/OutlineEffect.html
3. `.../three/examples/jsm/postprocessing/OutlinePass.js`; https://threejs.org/docs/pages/OutlinePass.html
4. `.../three/src/cameras/OrthographicCamera.js`; https://threejs.org/docs/pages/OrthographicCamera.html
5. `.../three/examples/jsm/loaders/GLTFLoader.js` (dòng 85–112, danh sách extension); https://threejs.org/docs/pages/GLTFLoader.html
6. https://threejs.org/examples/webgl_materials_toon.html , https://threejs.org/examples/webgl_postprocessing_outline.html , https://threejs.org/examples/webgl_loader_gltf.html , https://threejs.org/examples/webgl_camera.html
7. https://raw.githubusercontent.com/mrdoob/three.js/dev/manual/pages/rendering-on-demand.html (hiển thị tại https://threejs.org/manual/#en/rendering-on-demand)
8. `.../three/src/renderers/WebGLRenderer.js` (JSDoc `setAnimationLoop`, ~dòng 1600); https://threejs.org/docs/pages/WebGLRenderer.html
9. https://spline.design/pricing
10. https://registry.npmjs.org/@splinetool/runtime/latest
11. https://registry.npmjs.org/playcanvas/latest
12. https://unpkg.com/playcanvas/build/?meta
13. https://registry.npmjs.org/@babylonjs/core/latest
14. https://unpkg.com/babylonjs/?meta
15. https://godotengine.org/license/
16. https://docs.godotengine.org/en/stable/tutorials/export/exporting_for_web.html
17. https://docs.blender.org/manual/en/latest/advanced/command_line/render.html
18. https://docs.blender.org/manual/en/latest/advanced/command_line/arguments.html
19. https://docs.blender.org/api/current/bpy.ops.export_scene.html
20. https://docs.blender.org/manual/en/latest/addons/scene_gltf2.html
21. https://docs.blender.org/manual/en/latest/render/eevee/index.html
22. `/Users/erics/app/3dviz-pro-max/skills/3dviz-pro-max/templates/kits/blender/export-village-assets.py`
23. https://registry.npmjs.org/@gltf-transform/cli/latest
24. https://gltf-transform.dev/cli
25. `.../three/examples/jsm/{effects/OutlineEffect.js:7, postprocessing/OutlinePass.js:15, loaders/GLTFLoader.js:67, loaders/DRACOLoader.js:13}`; `.../three/package.json` `exports` (`./addons/*` → `./examples/jsm/*`)
26. https://raw.githubusercontent.com/mrdoob/three.js/dev/manual/pages/installation.html (hiển thị tại https://threejs.org/manual/#en/installation)
27. `.../three/examples/jsm/loaders/DRACOLoader.js:17-19`; `.../three/examples/jsm/libs/draco/README.md`
28. `/Users/erics/app/3dviz-pro-max/skills/3dviz-pro-max/data/knowledge/style-profile/{style-cel-shaded,style-tilt-shift-diorama,style-toy-miniature,style-low-poly}.json`
29. `/Users/erics/app/threejsvideo/README.md`, `package.json`, `references/README.md`, `references/export/*/metadata.json`
30. https://kenney.nl/assets/furniture-kit
31. https://kenney.nl/support (FAQ licence)
32. https://poly.pizza/bundle/Furniture-Kit-NoG1sEUD1z
33. https://kenney.nl/assets/mini-characters
34. https://kenney.nl/assets/city-kit-commercial
35. https://quaternius.com/packs/ultimatefurniture.html
36. https://quaternius.com/license.html
37. https://api.sketchfab.com/v3/licenses
38. https://www.anthropic.com/legal/trademark-guidelines
39. https://brandfolder.com/anthropic
40. https://github.com/badlogic/pi-mono (LICENSE, README.md qua GitHub API)
41. https://unpkg.com/three@0.180.0/build/?meta … https://unpkg.com/three@0.185.0/build/?meta ; https://registry.npmjs.org/three/0.180.0 (MIT, 1 117 file)
42. https://unpkg.com/three@0.186.0/build/?meta ; `/Users/erics/app/threejsvideo/node_modules/three/build/` (0.186.1)
43. https://raw.githubusercontent.com/wiki/mrdoob/three.js/Migration-Guide.md ; https://github.com/mrdoob/three.js/pull/25435 ; https://github.com/mrdoob/three.js/pull/29404 (codesplit `three.core.js`)
44. `/Users/erics/app/3dviz-pro-max/skills/3dviz-pro-max/scripts/capture.py` ; `docs/installation.md` ; `templates/kits/README.md`
45. `/Users/erics/app/threejsvideo/scripts/lib/browser.mjs`
46. Repo mate: `internal/dashboard/ui.go`, `internal/dashboard/ui_assets_test.go` (`TestUIMakesNoOffOriginRequest`), `internal/dashboard/ui_office_test.cjs`, `internal/dashboard/ui_office_test.go`, `docs/dashboard.md` §10–11, `docs/timeline.md` §9.1, `docs/archive/evidence/m6-dashboard-2026-09-21.md`
47. `/Users/erics/app/mate-landing/dist/{index.html,styles.css,assets/hero-harnesses-3d.png}`
48. `/Applications/Blender.app/Contents/MacOS/Blender --version` và `-E help` (2026-10-24); thí nghiệm `/tmp/mate-research/blender/`
49. `~/.claude/plugins/marketplaces/topview-3d-builder/skills/topview-3d-cli/SKILL.md`, `~/.claude/skills/muse2api-video/SKILL.md`

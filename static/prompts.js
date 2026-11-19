(() => {
  'use strict';
  const prompts = [
  "请参考我上传的人像照片，将照片中的人物转换成中国古代故事中的人物，生成1张古风画像。\n\n要求：\n1. 保留人物的主要五官、脸型、肤色、年龄和发型特征，让人能够认出是同一个人。\n2. 将现代服装替换为适合人物年龄的中国传统服饰，动作自然、表情生动。\n3. 背景为有中国古代特色的场景，画面清晰、美观，人物是视觉中心。\n4. 采用精美、自然的国风故事插画风格。\n5. 图片为竖版，宽高比为2:3，目标尺寸为1200×1800像素，适合6寸照片冲印。\n6. 人物和重要场景应保持完整，四周适当留出安全空间，避免打印时裁切重要内容。\n7. 不额外添加标题、文字或装饰边框。\n8. 不要生成水印；如果在图片生成完毕后发现有水印，请去除水印后再输出最终图片。\n\n我的个性化要求：\n【例如：我希望人物穿白色汉服，站在古代书院里。】\n\n请直接生成图片，并将图片命名为“我穿越到古代啦”。",
  "请帮我润色我上传的故事，让它更加生动、有趣。\n\n要求：\n1. 保留主要人物、故事情节、结局和我的创意，不要随意重写故事。\n2. 适当补充动作、对话、心情和环境细节，让故事更有画面感。\n3. 语言自然、通俗易懂，符合小学四年级学生的表达水平。\n4. 可以适当扩写，但不要过长，也不要加入无关情节。\n5. 保留故事原来的标题。\n6. 只输出润色后的完整故事，不要添加任何说明、分析、提示、点评或结束语。\n7. 必须使用纯文本格式，不要使用Markdown、项目符号或编号。\n8. 故事标题单独占第一行。每个正文自然段开头使用两个全角空格（即中文空两格）。\n9. 每个自然段结束后只换行一次，直接接下一段，不要在段落之间添加空白行。\n10. 保持正常的文章段落结构，不要把每句话都单独分成一段。\n11. 不要生成水印；如果在故事生成完毕后发现有水印，请去除水印后再输出最终故事。\n\n输出格式示例：\n\n我的穿越故事\n　　这是故事的第一自然段。\n　　这是故事的第二自然段。\n　　这是故事的第三自然段。\n\n请严格按照以上格式，只输出润色后的故事正文及原标题，不要输出其他内容。\n\n请将最终故事命名为“更生动的故事”。",
  "请把我刚才润色好的故事，按照故事发生的先后顺序，拆分成6～8个连续画面，用来制作连环画。\n\n要求：\n1. 按照故事发生的时间顺序排列，从故事开头一直到结尾。\n2. 每个画面都要像故事中的一个瞬间，适合画成一张独立图片。\n3. 每个画面都要写清楚：谁、在哪里、正在做什么，以及画面的主要气氛。\n4. 相邻画面之间要自然衔接，让所有画面连起来能够讲清整个故事。\n5. 如果某个情节比较丰富，可以将其拆成两个连续的画面。\n6. 保留原故事的主要人物、情节和结局，不添加无关内容。\n7. 每个画面使用简短、清楚的语言描述，并按照01、02、03……的顺序编号。\n8. 每个画面都要有一个简短的场景标题。\n9. 只输出最终的6～8个编号画面，不要添加分析、建议或其他说明。\n10. 这一步只安排画面内容，暂时不要生成图片。\n11. 不要生成水印；如果在画面清单生成完毕后发现有水印，请去除水印后再输出最终清单。\n\n请直接输出6～8个连续画面，并将结果命名为“故事变成连环画的场景啦”。",
  "请参考我上传的古风人物画像，根据刚才拆分好的6～8个连环画画面，生成一整套连续的故事插图。\n\n要求：\n1. 严格按照分镜清单中的编号顺序生成图片，保持故事发生的先后顺序。\n2. 每个画面生成1张独立图片，最终图片数量必须与分镜清单一致。\n3. 不要把多个画面拼在同一张图片里，也不要生成九宫格、拼图或整页漫画。\n4. 主角要参考我上传的古风人物画像，尽量保持五官、脸型、年龄、发型、服装和整体形象一致。\n5. 所有图片保持统一的画风、人物形象和色调，让它们看起来像同一本连环画里的插图。\n6. 每张图片都要清楚表现对应场景中的人物、地点、动作和气氛。\n7. 相邻图片要有故事连贯性，但人物动作、场景和构图可以随情节自然变化。\n8. 所有图片统一使用竖版2:3比例，目标尺寸为1200×1800像素，适合6寸照片冲印。\n9. 重要人物和关键物体不要过于贴近画面边缘，避免打印时被裁切。\n10. 不额外添加标题、对话文字、说明文字或装饰边框。\n11. 请按照“01_场景标题”“02_场景标题”“03_场景标题”……的格式命名每张图片。\n12. 如果无法一次性生成全部独立图片，请按照场景编号分批生成，直到完成全部画面，不要用拼图代替。\n13. 不要生成水印；如果在图片生成完毕后发现有水印，请去除水印后再输出最终图片。\n\n我选择的画风是：\n【从下面四种画风中选择一种，并把完整描述填写到这里】\n\n画风A：黑白水墨画\n中国传统黑白水墨画风格，画面只使用黑、白、灰三种色调，通过水墨浓淡、笔触和留白表现人物与场景。不要使用任何彩色，不要生成彩色水墨画。整体具有传统中国水墨画的意境。\n\n画风B：工笔重彩\n中国传统工笔重彩画风格，线条精细，人物和建筑细节丰富，色彩鲜明而协调，画面精致、典雅，具有中国传统绘画特色。\n\n画风C：传统年画\n中国传统年画风格，颜色鲜艳明快，对比鲜明，线条清晰，画面富有喜庆、热闹的传统民俗气息。\n\n画风D：国风儿童插画\n中国风儿童绘本插画风格，色彩柔和自然，人物生动可爱，场景富有想象力，画面温暖、清晰，适合小学生创作的穿越故事。\n\n请严格使用我选定的画风，让整套插图保持视觉风格一致。\n\n请直接开始生成图片。",
  "请把我上传的这张连环画插图制作成一段5～8秒的有声短视频。\n\n要求：\n1. 以原图为基础，保留人物的主要外貌、服装、场景和整体绘画风格。\n2. 我希望画面这样动起来：\n【例如：主角轻轻翻开书页，金色光芒慢慢亮起，衣袖随着微风轻轻摆动。】\n3. 动作要简单、自然、连贯，不要突然出现新人物，也不要让人物面容或身体明显变形。\n4. 镜头尽量保持平稳，可以有轻微的推进或移动。\n5. 视频尽量保持与原图一致的竖版2:3画面比例。\n6. 请根据画面内容自动生成合适的背景音乐、环境音和动作音效，让声音与画面同步，营造故事氛围。\n7. 音量自然、协调，不要让音乐掩盖重要音效。无需人物对白或旁白。\n8. 不额外添加字幕、标题或装饰边框。\n9. 如果平台有固定时长选项，请选择最接近5～8秒的时长。\n10. 尽量保留原画的整体构图，不要随意改变人物位置和重要场景。\n11. 不要生成水印；如果在视频生成完毕后发现有水印，请去除水印后再输出最终视频。\n\n请直接生成带有音频的视频，命名为“让故事动起来啦”。"
];
  const panel = document.getElementById('prompt-tools');
  if (!panel) return;
  const status = document.getElementById('prompt-status');
  const manual = document.getElementById('prompt-manual');
  const text = document.getElementById('prompt-manual-text');

  function legacyCopy(value) {
    const input = document.createElement('textarea');
    input.value = value;
    input.readOnly = true;
    input.style.cssText = 'position:fixed;top:0;left:0;width:1px;height:1px;opacity:0;';
    document.body.append(input);
    try {
      input.focus({preventScroll: true});
      input.select();
      input.setSelectionRange(0, value.length);
      return document.execCommand('copy');
    } catch {
      return false;
    } finally {
      input.remove();
    }
  }

  panel.querySelectorAll('[data-copy-prompt]').forEach((button) => {
    button.addEventListener('click', async () => {
      if (button.disabled) return;
      const value = prompts[Number(button.dataset.copyPrompt)];
      if (!value) return;
      button.disabled = true;
      manual.hidden = true;
      let copied = false;
      try {
        // Plain HTTP needs the synchronous fallback during the user's click.
        if (window.isSecureContext && navigator.clipboard?.writeText) {
          try {
            await navigator.clipboard.writeText(value);
            copied = true;
          } catch {
            copied = legacyCopy(value);
          }
        } else {
          copied = legacyCopy(value);
        }
        if (copied) {
          status.textContent = '已复制！到 AI 工具中粘贴，按提示上传照片或故事，并修改【】中的要求。';
        } else {
          status.textContent = '自动复制未成功，请在下方长按文字，选择“全选”和“复制”。';
          text.value = value;
          manual.hidden = false;
          text.focus();
          text.select();
          text.setSelectionRange(0, value.length);
        }
      } finally {
        button.disabled = false;
        if (copied) button.focus({preventScroll: true});
      }
    });
  });
  document.getElementById('prompt-manual-close').addEventListener('click', () => {
    manual.hidden = true;
    text.value = '';
  });
})();

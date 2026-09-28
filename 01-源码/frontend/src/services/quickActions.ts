/**
 * 快捷操作建议（方向2）：AI 面板输入框上方的一键提问模板。
 * 点击后把提示词填入输入框（用户可编辑后再发送），不直接发、不触发写操作。
 * 全部为「读/解释/校验」类，安全；措辞用「当前文件」指代，配合方向1 的上下文感知。
 */
export interface QuickAction {
  id: string;
  label: string;
  prompt: string;
}

export const BUILTIN_QUICK_ACTIONS: QuickAction[] = [
  {
    id: "explain-file",
    label: "解释当前文件",
    prompt: "解释当前打开的文件的作用和结构，重点说明其中的关键字段。",
  },
  {
    id: "explain-fields",
    label: "解释字段",
    prompt: "当前文件里的字段（如 [name]、[grade]、[explain] 等）分别是什么意思、有哪些取值？",
  },
  {
    id: "view-object",
    label: "查看对象",
    prompt: "用对象视图聚合查看当前文件对应的游戏对象（ID 与登记表）。",
  },
  {
    id: "check-file",
    label: "校验当前文件",
    prompt: "帮我检查当前文件有没有格式、引用、编码方面的问题。",
  },
  {
    id: "resolve-string",
    label: "解释字符串引用",
    prompt: "当前文件里的字符串表引用（如 <3::name_10018>）分别对应什么译文？逐个解释。",
  },
];

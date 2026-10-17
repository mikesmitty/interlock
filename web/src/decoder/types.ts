// The decoder definition the server resolves from JMRI's files: see
// internal/decoder/definition.go and panes.go for what each field means.

export interface DecoderDefinition {
  file: string;
  family: string;
  manufacturer: string;
  model: string;
  product_ids?: string[];
  num_outs: number;
  num_fns: number;
  max_fn_num: number;
  esu_function_map?: string;
  programming: DecoderProgramming;
  variables: DecoderVariable[];
  cvs: DecoderCV[];
  panes: DecoderPane[];
  function_labels?: { num: number; label: string; lockable: boolean }[];
  resets?: { label: string; cv: string; value: number }[];
  missing?: string[];
  problems?: string[];
}

export interface DecoderProgramming {
  direct: string;
  paged: boolean;
  register: boolean;
  no_decoder?: boolean;
  modes?: string[];
  index?: { pi: number; si: number; cv_first: boolean; cache: boolean };
  tcs?: boolean;
  ops_write_delay?: number;
}

export interface DecoderCV {
  name: string;
  read_only?: boolean;
  write_only?: boolean;
  info_only?: boolean;
}

export type VariableType =
  | "decimal"
  | "hex"
  | "enum"
  | "split"
  | "split_hex"
  | "split_enum"
  | "split_text"
  | "split_date_time"
  | "split_hundreds"
  | "speed_table"
  | "short_address"
  | "long_address"
  | "composite"
  | "constant";

export interface Part {
  cv: string;
  mask?: number;
  shift?: number;
  width?: number;
  radix?: number;
}

export interface Choice {
  label: string;
  value: number;
  group?: string;
}

export interface Qualifier {
  item?: string;
  relation: "eq" | "ne" | "gt" | "ge" | "lt" | "le" | "never";
  value?: number;
}

export interface DecoderVariable {
  item: string;
  label: string;
  tooltip?: string;
  comment?: string;
  type: VariableType;
  parts?: Part[];
  read_only?: boolean;
  write_only?: boolean;
  info_only?: boolean;
  ops_only?: boolean;
  default?: string;
  qualifiers?: Qualifier[];
  min: number;
  max?: number;
  offset?: number;
  factor?: number;
  modulus?: number;
  case?: string;
  choices?: Choice[];
  text?: { term_byte?: number; pad_byte?: number; match?: string };
  date_time?: { base: string; unit: string; factor: number; display: string };
  speed_table?: { entries: number; min: number; max: number; mfx?: boolean };
  composite?: { label: string; settings: { item: string; value: number }[] }[];
  address_changes?: string[];
}

export interface DecoderPane {
  name: string;
  tooltip?: string;
  qualifiers?: Qualifier[];
  nodes: DecoderNode[];
  items: string[];
  all_cvs?: boolean;
}

export interface DecoderNode {
  kind:
    | "column"
    | "row"
    | "group"
    | "grid"
    | "griditem"
    | "display"
    | "label"
    | "separator"
    | "dccaddress"
    | "fnmap"
    | "esu_fnmap"
    | "cvtable";
  children?: DecoderNode[];
  item?: string;
  label?: string;
  tooltip?: string;
  format?: string;
  layout?: "right" | "above" | "below";
  view_only?: boolean;
  text?: string;
  qualifiers?: Qualifier[];
  grid?: { x: number; y: number; w: number; h: number };
  fnmap?: {
    columns: { name: string; label?: string }[];
    rows: { label: string; cells: (string[] | null)[] }[];
  };
  esu_map?: ESUMap;
  items?: string[];
}

/** An ESU decoder's mapping table; see internal/decoder/esumap.go. */
export interface ESUMap {
  rows: number;
  blocks: ESUBlock[];
}

export interface ESUBlock {
  name: string;
  bits: number;
  page: number;
  start: number;
  items: ESUItem[];
}

export interface ESUItem {
  name: string;
  on?: string;
  off?: string;
  hidden?: boolean;
}

export interface DecoderIdentity {
  manufacturer: number;
  version: number;
  product_id?: number;
  has_product_id: boolean;
}

export interface DecoderCandidate {
  family: string;
  manufacturer: string;
  model: string;
  file: string;
  exact: boolean;
}

export interface DecoderIndex {
  version: string;
  manufacturers: { name: string; id: number }[];
  families: {
    name: string;
    manufacturer: string;
    file: string;
    low_version: number;
    high_version: number;
    comment?: string;
    type?: string;
    modes?: string;
    models: { name: string; product_ids?: string[]; show?: string; comment?: string }[];
  }[];
}

export interface DecoderSource {
  source: "github" | "archive" | "folder";
  ref?: string;
  path?: string;
  archive?: { state: string; bytes: number; files: number; error?: string };
}

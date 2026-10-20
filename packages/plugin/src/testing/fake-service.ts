// A Component API for tests: package ddash.fake (Component "fake"), one
// getter per refresh tier, one Action (Poke) and one disruptive Action (Zap), each taking Ping and answering
// Pong.
import { create, createFileRegistry, setExtension, type DescMethodUnary } from "@bufbuild/protobuf";
import {
  FileDescriptorProtoSchema,
  FieldDescriptorProto_Label,
  FieldDescriptorProto_Type,
  MethodOptionsSchema,
  type MethodOptions,
} from "@bufbuild/protobuf/wkt";
import {
  action,
  ActionSchema,
  file_ddash_options,
  getter,
  Getter_Refresh,
  GetterSchema,
} from "@ddash/proto/ddash/options_pb";

function getterOptions(refresh: Getter_Refresh): MethodOptions {
  const o = create(MethodOptionsSchema);
  setExtension(o, getter, create(GetterSchema, { refresh }));
  return o;
}

function actionOptions(disruptive = false): MethodOptions {
  const o = create(MethodOptionsSchema);
  setExtension(o, action, create(ActionSchema, { disruptive }));
  return o;
}

const method = (name: string, options: MethodOptions) => ({
  name,
  inputType: ".ddash.fake.Ping",
  outputType: ".ddash.fake.Pong",
  options,
});

const file = create(FileDescriptorProtoSchema, {
  name: "fake/fake.proto",
  package: "ddash.fake",
  syntax: "proto3",
  dependency: ["ddash/options.proto"],
  messageType: [
    {
      name: "Ping",
      field: [{ name: "host", jsonName: "host", number: 1, type: FieldDescriptorProto_Type.STRING, label: FieldDescriptorProto_Label.OPTIONAL }],
    },
    {
      name: "Pong",
      field: [{ name: "n", jsonName: "n", number: 1, type: FieldDescriptorProto_Type.INT32, label: FieldDescriptorProto_Label.OPTIONAL }],
    },
  ],
  service: [
    {
      name: "FakeService",
      method: [
        method("Live", getterOptions(Getter_Refresh.LIVE)),
        method("Fast", getterOptions(Getter_Refresh.FAST)),
        method("Slow", getterOptions(Getter_Refresh.SLOW)),
        method("None", getterOptions(Getter_Refresh.NONE)),
        method("Poke", actionOptions()),
        method("Zap", actionOptions(true)),
      ],
    },
  ],
});

const registry = createFileRegistry(file, (name) => (name === "ddash/options.proto" ? file_ddash_options : undefined));

const service = registry.getService("ddash.fake.FakeService")!;
const unary = (name: string) => service.method[name] as DescMethodUnary;

/** Shaped like generated code's service: FakeService.method.fast. */
export const FakeService = {
  service,
  method: { live: unary("live"), fast: unary("fast"), slow: unary("slow"), none: unary("none"), poke: unary("poke"), zap: unary("zap") },
};

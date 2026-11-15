package containerpb

// LabelProtected is the container label that, when "true", makes a
// container Protected: ddash doesn't stop, restart or update it without
// an explicit override. Hand-written, beside the generated message whose
// protected field it sets.
const LabelProtected = "ddash.protected"

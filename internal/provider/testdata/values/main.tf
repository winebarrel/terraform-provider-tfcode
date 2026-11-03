locals {
  list  = tolist(["a"])
  map   = tomap({ a = "x" })
  set   = toset(["a"])
  empty = tolist([])

  list_of_lists   = tolist([tolist([1])])
  list_of_sets    = tolist([toset(["a"])])
  list_of_maps    = tolist([tomap({ a = true })])
  list_of_objects = tolist([{ a = 1 }])

  null_string = tostring(null)
  null_number = tonumber(null)
  null_bool   = tobool(null)

  with_null_string = tolist(["a", null])
  with_null_number = tolist([1, null])
  with_null_bool   = tolist([true, null])
  with_null_list   = tolist([tolist(["a"]), tolist(null)])
  with_null_set    = tolist([toset(["a"]), toset(null)])
  with_null_map    = tolist([tomap({ a = "x" }), tomap(null)])
  with_null_object = tomap({ a = { b = 1 }, c = null })
  with_null_tuple  = tomap({ a = ["x", 1], c = null })
}

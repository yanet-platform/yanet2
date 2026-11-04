//! `#[yanet_sdk::api]`.
//!
//! Every `pub fn` of the impl becomes an `extern "C"` export that converts
//! its arguments, calls the method under the guards of `yanet-sdk-api` and
//! reports a failure in a trailing `yanet_error` argument. The impl is the api
//! of a module, whose builds return configurations, or of an object type,
//! whose builds return objects; a method returning `Result<u64, _>` is a
//! query of either.
//!
//! The generated code carries `Span::mixed_site()`, so it is macro output
//! for the lints of the api crate: `#![forbid(unsafe_code)]` does not see
//! its `unsafe`, and a generated `allow` is not needed (and `forbid` would
//! refuse it).

use proc_macro2::{Span, TokenStream};
use quote::{quote, quote_spanned};
use syn::{
    FnArg, Ident, ImplItem, ImplItemFn, ItemImpl, Lifetime, Path, ReturnType, Type, TypeReference, Visibility,
    parse::{Parse, ParseStream},
    spanned::Spanned,
};

/// The attribute: what the impl builds, `module = <path>` for a module's
/// configurations or `object = <path>` for objects of the type.
pub enum Args {
    Module(Path),
    Object(Path),
}

impl Parse for Args {
    fn parse(input: ParseStream) -> syn::Result<Self> {
        let key: Ident = input.parse()?;
        let kind: fn(Path) -> Self = match key.to_string().as_str() {
            "module" => Self::Module,
            "object" => Self::Object,
            _ => {
                return Err(syn::Error::new(
                    key.span(),
                    "expected `module = <Module type>` or `object = <Object type>`",
                ));
            }
        };
        input.parse::<syn::Token![=]>()?;
        let target = input.parse()?;
        if !input.is_empty() {
            return Err(input.error("unexpected tokens after the type"));
        }
        Ok(kind(target))
    }
}

const INTS: [&str; 9] = ["u8", "u16", "u32", "u64", "i8", "i16", "i32", "i64", "usize"];

/// How a parameter crosses the C boundary.
enum Lowered<'a> {
    Agent,
    Str,
    /// `&BuiltObject<O, Agent>`, the type as written.
    Object(&'a Type),
    Record(&'a Type),
    Slice(&'a Type),
    Int(&'a Type),
}

/// The last segment's identifier of a path type without a qualified self.
fn simple_ident(ty: &Type) -> Option<&Ident> {
    match ty {
        Type::Path(path) if path.qself.is_none() => path.path.segments.last().map(|segment| &segment.ident),
        _ => None,
    }
}

fn lower(ty: &Type) -> syn::Result<Lowered<'_>> {
    match ty {
        Type::Reference(TypeReference { mutability: Some(_), .. }) => Err(syn::Error::new(
            ty.span(),
            "a mutable reference does not cross the C boundary: take `&T`",
        )),
        Type::Reference(TypeReference {
            lifetime: Some(Lifetime { ident, .. }),
            ..
        }) if ident != "_" => Err(syn::Error::new(
            ty.span(),
            "a named lifetime lets the method keep what C lent for the call: elide it",
        )),
        Type::Reference(reference) => Ok(match &*reference.elem {
            elem if simple_ident(elem).is_some_and(|ident| ident == "Agent") => Lowered::Agent,
            elem if simple_ident(elem).is_some_and(|ident| ident == "str") => Lowered::Str,
            elem if simple_ident(elem).is_some_and(|ident| ident == "BuiltObject") => Lowered::Object(elem),
            Type::Slice(slice) => Lowered::Slice(&slice.elem),
            elem => Lowered::Record(elem),
        }),
        Type::Path(_) if simple_ident(ty).is_some_and(|ident| INTS.iter().any(|int| ident == int)) => {
            Ok(Lowered::Int(ty))
        }
        _ => Err(syn::Error::new(
            ty.span(),
            "unsupported parameter type: use `&Agent`, `&str`, `&BuiltObject<O, Agent>`, `&Record`, `&[Record]` or \
             u8..u64, i8..i64, usize",
        )),
    }
}

/// Snake case of a CamelCase identifier: each upper case letter after the
/// first starts a word.
fn snake(name: &str) -> String {
    let mut out = String::new();
    for (index, ch) in name.chars().enumerate() {
        if ch.is_ascii_uppercase() {
            if index != 0 {
                out.push('_');
            }
            out.push(ch.to_ascii_lowercase());
        } else {
            out.push(ch);
        }
    }
    out
}

/// Whether a return type is `Result<u64, _>`, that of a query: a method that
/// reads a value, such as a field of an object, rather than building. Its
/// export writes the value through a `uint64_t *out` before the error
/// argument and returns 0, or -1 with the error set.
fn is_query(output: &ReturnType) -> bool {
    let ReturnType::Type(_, ty) = output else {
        return false;
    };
    let Type::Path(path) = &**ty else {
        return false;
    };
    let Some(segment) = path.path.segments.last() else {
        return false;
    };
    let syn::PathArguments::AngleBracketed(args) = &segment.arguments else {
        return false;
    };
    segment.ident == "Result"
        && matches!(args.args.first(), Some(syn::GenericArgument::Type(ty)) if simple_ident(ty).is_some_and(|ident| ident == "u64"))
}

/// One exported method.
fn export(stem: &str, attr: &Args, self_ty: &Ident, method: &ImplItemFn) -> syn::Result<TokenStream> {
    let ms = Span::mixed_site();
    let sig = &method.sig;
    if sig.receiver().is_some() {
        return Err(syn::Error::new(
            sig.span(),
            "an api method is an associated function: it has no `self`",
        ));
    }
    if sig.constness.is_some() || sig.asyncness.is_some() || sig.unsafety.is_some() || sig.abi.is_some() {
        return Err(syn::Error::new(sig.span(), "an api method is a plain `fn`"));
    }
    if !sig.generics.params.is_empty() || sig.generics.where_clause.is_some() || sig.variadic.is_some() {
        return Err(syn::Error::new(sig.generics.span(), "an api method is not generic"));
    }
    let name = &sig.ident;
    if name == "free" {
        return Err(syn::Error::new(
            name.span(),
            "`free` is the export the macro adds to the impl",
        ));
    }
    let symbol = Ident::new(&format!("{stem}_{name}"), ms);
    let p = quote_spanned!(ms=> ::yanet_sdk_api::__private);

    let mut agent_params = Vec::new();
    let mut c_params = Vec::new();
    let mut convert = Vec::new();
    let mut args = Vec::new();
    for (index, arg) in sig.inputs.iter().enumerate() {
        let FnArg::Typed(arg) = arg else {
            unreachable!("receivers are refused above")
        };
        let ty = &*arg.ty;
        let c = Ident::new(&format!("__a{index}"), ms);
        let v = Ident::new(&format!("__v{index}"), ms);
        let label = match &*arg.pat {
            syn::Pat::Ident(pat) => pat.ident.to_string(),
            _ => format!("#{index}"),
        };
        match lower(ty)? {
            Lowered::Agent => {
                agent_params.push(c.clone());
                c_params.push(quote_spanned!(ms=> #c: *mut #p::agent));
                args.push(quote_spanned!(ms=> __agent));
                continue;
            }
            Lowered::Str => {
                c_params.push(quote_spanned!(ms=> #c: *const ::core::ffi::c_char));
                convert.push(quote_spanned!(ms=> let #v = #p::c_str(#c, #label)?;));
            }
            Lowered::Object(object) => {
                let Args::Object(target) = attr else {
                    return Err(syn::Error::new(
                        ty.span(),
                        "a module api takes no object: only an object api reads its objects",
                    ));
                };
                c_params.push(quote_spanned!(ms=> #c: *mut #p::cp_object));
                convert.push(quote_spanned!(ms=>
                    let #v: #p::BuiltObject<#target, #p::Agent> = #p::handle(#c, #label)?;
                ));
                // The annotation makes an object of another type than the
                // attribute's a type mismatch reported at the parameter.
                convert.push(quote_spanned!(object.span()=> let #v: &#object = &#v;));
            }
            Lowered::Record(record) => {
                c_params.push(quote_spanned!(ms=> #c: *const #record));
                convert.push(quote_spanned!(record.span()=> let #v = #p::record::<#record>(#c, #label)?;));
            }
            Lowered::Slice(record) => {
                let len = Ident::new(&format!("__a{index}_len"), ms);
                c_params.push(quote_spanned!(ms=> #c: *const #record, #len: usize));
                convert.push(quote_spanned!(record.span()=> let #v = #p::slice_of::<#record>(#c, #len, #label)?;));
            }
            Lowered::Int(int) => {
                c_params.push(quote_spanned!(ms=> #c: #int));
                args.push(quote_spanned!(ms=> #c));
                continue;
            }
        }
        args.push(quote_spanned!(ms=> #v));
    }
    let [agent_param] = agent_params.as_slice() else {
        return Err(syn::Error::new(sig.span(), "an api method takes exactly one `&Agent`"));
    };

    // The annotation makes another module's configuration or object, or any
    // other result type, a type mismatch reported at the return type.
    let ret_span = match &sig.output {
        ReturnType::Type(_, ty) => ty.span(),
        ReturnType::Default => name.span(),
    };
    let doc = format!(" C export of `{self_ty}::{name}`.");
    if is_query(&sig.output) {
        let ret = quote_spanned!(ret_span=> ::core::result::Result<u64, #p::ApiError>);
        return Ok(quote_spanned! {ms=>
            #[doc = #doc]
            #[unsafe(no_mangle)]
            pub unsafe extern "C" fn #symbol(
                #(#c_params,)*
                __out: *mut u64,
                __err: *mut *mut #p::yanet_error,
            ) -> ::core::ffi::c_int {
                unsafe {
                    #p::query(#agent_param, __out, __err, |__agent| {
                        #(#convert)*
                        let __value: #ret = #self_ty::#name(#(#args),*);
                        __value
                    })
                }
            }
        });
    }
    let (built, header, target) = match attr {
        Args::Module(target) => (quote!(Built), quote!(cp_module), target),
        Args::Object(target) => (quote!(BuiltObject), quote!(cp_object), target),
    };
    let ret = quote_spanned! {ret_span=>
        ::core::result::Result<#p::#built<#target, #p::Agent>, #p::ApiError>
    };
    Ok(quote_spanned! {ms=>
        #[doc = #doc]
        #[unsafe(no_mangle)]
        pub unsafe extern "C" fn #symbol(
            #(#c_params,)*
            __err: *mut *mut #p::yanet_error,
        ) -> *mut #p::#header {
            unsafe {
                #p::build(#agent_param, __err, |__agent| {
                    #(#convert)*
                    let __built: #ret = #self_ty::#name(#(#args),*);
                    __built
                })
            }
        }
    })
}

pub fn expand(args: Args, item: ItemImpl) -> syn::Result<TokenStream> {
    let ms = Span::mixed_site();
    if item.trait_.is_some() || !item.generics.params.is_empty() || item.generics.where_clause.is_some() {
        return Err(syn::Error::new(
            item.impl_token.span(),
            "#[api] goes on an inherent, non-generic impl of a local type",
        ));
    }
    let self_ty = match &*item.self_ty {
        Type::Path(path) if path.qself.is_none() && path.path.get_ident().is_some() => path.path.get_ident(),
        _ => None,
    };
    let Some(self_ty) = self_ty else {
        return Err(syn::Error::new(
            item.self_ty.span(),
            "#[api] needs a local type named by its identifier",
        ));
    };
    let crate_name = std::env::var("CARGO_CRATE_NAME").map_err(|_| {
        syn::Error::new(
            Span::call_site(),
            "#[api] needs CARGO_CRATE_NAME: build the api crate with cargo",
        )
    })?;
    if crate_name.contains("__") || crate_name.ends_with('_') {
        return Err(syn::Error::new(
            Span::call_site(),
            format!(
                "the api crate name `{crate_name}` contains `__` or ends in `_`: its export prefix would not be unique"
            ),
        ));
    }
    let stem = format!("yanet_cp_{crate_name}__{}", snake(&self_ty.to_string()));
    let exports = item
        .items
        .iter()
        .filter_map(|member| match member {
            ImplItem::Fn(method) if matches!(method.vis, Visibility::Public(_)) => Some(method),
            _ => None,
        })
        .map(|method| export(&stem, &args, self_ty, method))
        .collect::<syn::Result<Vec<_>>>()?;
    let free = Ident::new(&format!("{stem}_free"), ms);
    let p = quote_spanned!(ms=> ::yanet_sdk_api::__private);
    let free_fn = match &args {
        Args::Object(target) => {
            let doc = format!(" Frees an object `{self_ty}` built, as a C object's owner does.");
            quote_spanned! {ms=>
                #[doc = #doc]
                #[unsafe(no_mangle)]
                pub unsafe extern "C" fn #free(
                    __object: *mut #p::cp_object,
                    __err: *mut *mut #p::yanet_error,
                ) -> ::core::ffi::c_int {
                    unsafe { #p::free::<#p::BuiltObject<#target, #p::Agent>>(__object, __err) }
                }
            }
        }
        Args::Module(target) => {
            let doc = format!(" Frees a configuration `{self_ty}` built, as a C module's free does.");
            quote_spanned! {ms=>
                #[doc = #doc]
                #[unsafe(no_mangle)]
                pub unsafe extern "C" fn #free(
                    __module: *mut #p::cp_module,
                    __err: *mut *mut #p::yanet_error,
                ) -> ::core::ffi::c_int {
                    unsafe { #p::free::<#p::Built<#target, #p::Agent>>(__module, __err) }
                }
            }
        }
    };
    Ok(quote_spanned! {ms=>
        #item

        #(#exports)*

        #free_fn
    })
}
